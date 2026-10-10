package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
)

const sideChatBoundary = "You are in an independent side conversation. All inherited history from the parent conversation is reference context only, not active instructions. Do not continue the parent's tasks, plans, tool calls, approvals, or edits. Only new messages submitted in this side conversation are active user instructions. Do not spawn, interact with, or continue any subagents from this side conversation. You may use available skills and tools under the existing permissions. Do not modify the workspace unless explicitly requested in this side conversation. The workspace is shared with the parent; avoid disrupting its work."

type SideChatLaunch struct {
	CLISessionID, ResumePath, ResumeAt, Boundary string
}
type sideChat struct {
	id, parentID    string
	launch          SideChatLaunch
	info            sessionInfo
	started, closed bool
	rejectedRuntime string
}

func isSideChatID(id string) bool { return strings.HasPrefix(id, "side-") }

var sideChatUUID = regexp.MustCompile(`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}|00000000-0000-0000-0000-000000000000|ffffffff-ffff-ffff-ffff-ffffffffffff)$`)

func cloneSideChat(side *sideChat) *sideChat {
	if side == nil {
		return nil
	}
	copy := *side
	copy.info = cloneSessionInfo(side.info)
	return &copy
}

func (s *Server) sideChatInfo(id string) (sessionInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	side := s.sideChats[id]
	if side == nil || side.closed {
		return sessionInfo{}, false
	}
	return cloneSessionInfo(side.info), true
}
func sideChatResponse(side *sideChat) any {
	return map[string]any{"sessionId": side.id, "parentSessionId": side.parentID, "workDir": side.info.WorkDir, "title": side.info.Title, "ephemeral": true}
}
func (s *Server) sideChatsHTTP(w http.ResponseWriter, r *http.Request, parent string, parts []string) error {
	if isSideChatID(parent) {
		return badRequest("Nested side chats are not supported")
	}
	child := ""
	if len(parts) > 4 {
		child = parts[4]
	}
	if r.Method == http.MethodDelete && child != "" {
		s.mu.Lock()
		side := s.sideChats[child]
		if side == nil || side.parentID != parent {
			s.mu.Unlock()
			return notFound("Side chat not found")
		}
		side.closed = true
		s.mu.Unlock()
		if e := s.stopSessionAndWait(r.Context(), child); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"sessionId": child, "closed": true})
		return nil
	}
	if r.Method != http.MethodPost || child != "" {
		w.WriteHeader(405)
		return nil
	}
	data, e := io.ReadAll(r.Body)
	if e != nil {
		return e
	}
	if len(utf16.Encode([]rune(string(data)))) > 1024 {
		return badRequest("Side chat request is too large")
	}
	var input map[string]json.RawMessage
	if len(data) == 0 {
		input = map[string]json.RawMessage{}
	} else if !json.Valid(data) {
		return badRequest("Invalid JSON")
	} else if json.Unmarshal(data, &input) != nil || input == nil {
		return badRequest("Invalid side chat request")
	}
	for key := range input {
		if key != "sideChatId" {
			return badRequest("Invalid side chat request")
		}
	}
	cliID := ""
	if raw, exists := input["sideChatId"]; exists {
		if json.Unmarshal(raw, &cliID) != nil || !sideChatUUID.MatchString(cliID) {
			return badRequest("Invalid side chat request")
		}
	}
	if cliID == "" {
		cliID, e = uuid()
		if e != nil {
			return e
		}
	}
	id := "side-" + cliID
	lookup := func() (*sideChat, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		existing := s.sideChats[id]
		if existing != nil && (existing.parentID != parent || existing.closed) {
			return nil, &apiError{409, "CONFLICT", "Side chat ID is already used"}
		}
		return cloneSideChat(existing), nil
	}
	if existing, e := lookup(); e != nil {
		return e
	} else if existing != nil {
		writeJSON(w, 200, sideChatResponse(existing))
		return nil
	}
	info, e := s.findSession(r.Context(), parent)
	if e != nil {
		return notFound("Parent session not found")
	}
	if info.memory {
		return &apiError{409, "CONFLICT", "Start a conversation before opening a side chat"}
	}
	limit := float64(100)
	page, e := s.reader.ReadPage(r.Context(), info.Path, sessionstore.PageOptions{Limit: &limit})
	if e != nil {
		return e
	}
	messages, _ := projectHistory(page.Entries)
	boundary := ""
	for i := len(messages) - 1; i >= 0; i-- {
		row := historyRecord(messages[i])
		if stringField(row, "type") != "system" {
			boundary = stringField(row, "id")
			break
		}
	}
	if boundary == "" {
		return &apiError{409, "CONFLICT", "Start a conversation before opening a side chat"}
	}
	meta := historyRecord(info.Metadata)
	delete(meta, "repository")
	delete(meta, "worktreeSession")
	meta["customTitle"] = json.RawMessage(`"Side chat"`)
	meta["transcriptMessageCount"] = json.RawMessage("0")
	metadata, _ := json.Marshal(meta)
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	root := info.WorkDir
	entry := &sideChat{id: id, parentID: parent, launch: SideChatLaunch{CLISessionID: cliID, ResumePath: info.Path, ResumeAt: boundary, Boundary: sideChatBoundary}, info: sessionInfo{ID: id, Title: "Side chat", CreatedAt: now, ModifiedAt: now, ProjectPath: info.ProjectPath, ProjectRoot: &root, WorkDir: info.WorkDir, WorkDirExists: true, WorkspaceState: "available", PermissionMode: info.PermissionMode, RuntimeProviderID: append(json.RawMessage(nil), info.RuntimeProviderID...), RuntimeModelID: info.RuntimeModelID, EffortLevel: info.EffortLevel, Metadata: metadata, memory: true, ephemeral: true}}
	s.mu.Lock()
	existing := s.sideChats[id]
	if existing == nil {
		if s.sideChats == nil {
			s.sideChats = map[string]*sideChat{}
		}
		s.sideChats[id] = entry
		existing = entry
	}
	closed := existing.closed
	other := existing.parentID != parent
	existing = cloneSideChat(existing)
	s.mu.Unlock()
	if closed || other {
		return &apiError{409, "CONFLICT", "Side chat ID is already used"}
	}
	writeJSON(w, 200, sideChatResponse(existing))
	return nil
}
func (s *Server) prepareSideChatStart(id string) error {
	if !isSideChatID(id) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	side := s.sideChats[id]
	if side == nil || side.closed || side.started {
		return &apiError{410, "SESSION_DELETED", "This temporary side chat has expired. Open a new side chat."}
	}
	// This is the in-process equivalent of Source's successful CLI spawn. A
	// temporary child is never silently reconstructed from stale parent history.
	side.started = true
	return nil
}
func (s *Server) sideChatOptions(id string) *SideChatLaunch {
	s.mu.Lock()
	defer s.mu.Unlock()
	if side := s.sideChats[id]; side != nil && !side.closed {
		launch := side.launch
		return &launch
	}
	return nil
}
func (s *Server) closeSideChatsForParent(ctx context.Context, parent string) error {
	s.mu.Lock()
	ids := []string{}
	for id, side := range s.sideChats {
		if side.parentID == parent && !side.closed {
			side.closed = true
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		if e := s.stopSessionAndWait(ctx, id); e != nil {
			return e
		}
	}
	return nil
}

// Source does not spawn an unopened child merely to select a runtime. Preserve
// that admission boundary; an already opening/running child uses its live host.
func (s *Server) selectUnopenedSideChatRuntime(id string, runtime *runtimeSession, input protocol.JSONObject) (bool, error) {
	if !isSideChatID(id) {
		return false, nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.live != nil || runtime.opening != nil {
		return false, nil
	}
	if runtime.retired || runtime.ctx.Err() != nil {
		return true, context.Canceled
	}
	s.mu.Lock()
	side := s.sideChats[id]
	available := side != nil && !side.closed && !side.started
	s.mu.Unlock()
	if !available {
		return false, nil
	}
	var values storage.Object
	data, e := input.MarshalJSON()
	if e != nil {
		return true, e
	}
	if e := values.UnmarshalJSON(data); e != nil {
		return true, e
	}
	next, e := providerconfig.NormalizeRuntimeSelection(runtime.ctx, providerconfig.Options{Store: s.options.Store, ConfigDir: s.options.ConfigDir, Managed: s.options.Managed, Settings: s.options.Settings}, values)
	if e != nil {
		var api *settings.APIError
		if errors.As(e, &api) && api.Code == "RUNTIME_CONFIG_INVALID" {
			s.mu.Lock()
			side.rejectedRuntime = api.Message
			s.mu.Unlock()
		}
		return true, e
	}
	metadata := map[string]any{"runtimeProviderId": next.ProviderID, "runtimeModelId": next.ModelID}
	if next.EffortLevel != "" {
		metadata["effortLevel"] = next.EffortLevel
	}
	data, e = json.Marshal(metadata)
	if e != nil {
		return true, e
	}
	if e = s.updateLaunchMetadata(runtime.ctx, id, data); e != nil {
		return true, e
	}
	s.mu.Lock()
	side.rejectedRuntime = ""
	s.mu.Unlock()
	frame := map[string]any{"type": "runtime_config_applied", "providerId": next.ProviderID, "modelId": next.ModelID}
	if next.EffortLevel != "" {
		frame["effortLevel"] = next.EffortLevel
	}
	if next.RequestedConfig != nil {
		frame["requestedConfig"] = next.RequestedConfig
	}
	data, e = json.Marshal(frame)
	if e != nil {
		return true, e
	}
	s.broadcast(id, data)
	return true, nil
}
