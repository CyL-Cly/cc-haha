package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
)

type sessionInfo struct {
	ID                string          `json:"id"`
	Title             string          `json:"title"`
	CreatedAt         string          `json:"createdAt"`
	ModifiedAt        string          `json:"modifiedAt"`
	MessageCount      int             `json:"messageCount"`
	ProjectPath       string          `json:"projectPath"`
	ProjectRoot       *string         `json:"projectRoot"`
	WorkDir           string          `json:"workDir"`
	WorkDirExists     bool            `json:"workDirExists"`
	WorkspaceState    string          `json:"workspaceState"`
	PermissionMode    string          `json:"permissionMode,omitempty"`
	RuntimeProviderID json.RawMessage `json:"runtimeProviderId,omitempty"`
	RuntimeModelID    string          `json:"runtimeModelId,omitempty"`
	EffortLevel       string          `json:"effortLevel,omitempty"`
	Path              string          `json:"-"`
	teamWorker        bool
	Metadata          json.RawMessage `json:"-"`
	memory            bool
	ephemeral         bool
	workDirNull       bool
}

func (info sessionInfo) MarshalJSON() ([]byte, error) {
	type plain sessionInfo
	data, e := json.Marshal(plain(info))
	if e != nil || !info.workDirNull {
		return data, e
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(data, &fields); e != nil {
		return nil, e
	}
	fields["workDir"] = json.RawMessage("null")
	return json.Marshal(fields)
}

func uuid() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]), nil
}

func jsonObject(request *http.Request) (map[string]json.RawMessage, error) {
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, badRequest("Invalid JSON body")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, badRequest("Invalid JSON body")
	}
	return object, nil
}
func stringField(object map[string]json.RawMessage, name string) string {
	var value string
	_ = json.Unmarshal(object[name], &value)
	return value
}

func (server *Server) createSession(request *http.Request) (any, error) {
	body, err := jsonObject(request)
	if err != nil {
		return nil, err
	}
	workDir := stringField(body, "workDir")
	if raw, ok := body["workDir"]; ok && sourceJSONTruthy(raw) && workDir == "" {
		return nil, badRequest("workDir must be a string")
	}
	permissionMode := stringField(body, "permissionMode")
	if raw, ok := body["permissionMode"]; ok {
		var value string
		if json.Unmarshal(raw, &value) != nil || string(raw) == "null" {
			return nil, badRequest("permissionMode must be a string")
		}
	}
	if _, ok := body["permissionMode"]; ok && !settings.IsValidPermissionMode(permissionMode) {
		return nil, badRequest("Invalid permission mode: \"" + permissionMode + "\"")
	}
	var repositoryOptions *sessionRepositoryOptions
	if raw, ok := body["repository"]; ok {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return nil, badRequest("repository must be an object")
		}
		repositoryOptions = &sessionRepositoryOptions{}
		if branch, ok := object["branch"]; ok && string(branch) != "null" {
			if json.Unmarshal(branch, &repositoryOptions.Branch) != nil {
				return nil, badRequest("repository.branch must be a string")
			}
		}
		if value, ok := object["worktree"]; ok {
			if string(value) != "true" && string(value) != "false" {
				return nil, badRequest("repository.worktree must be a boolean")
			}
			repositoryOptions.Worktree = string(value) == "true"
		}
	}
	if workDir == "" {
		workDir = server.options.HomeDir
	}
	persist, err := server.shouldPersist(request.Context())
	if err != nil {
		return nil, err
	}
	id, err := uuid()
	if err != nil {
		return nil, err
	}
	prepared, err := server.resolveSessionRepository(request.Context(), workDir, repositoryOptions, id)
	if err != nil {
		return nil, err
	}
	workDir = prepared.WorkDir
	server.RegisterFilesystemAccessRoot(workDir)
	messageID, err := uuid()
	if err != nil {
		return nil, err
	}
	snapshotID, err := uuid()
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(server.options.ConfigDir, "projects", projectpath.Sanitize(workDir))
	if persist {
		current, err := server.shouldPersist(request.Context())
		if err != nil {
			return nil, err
		}
		if current {
			if err := os.MkdirAll(directory, 0o700); err != nil {
				return nil, err
			}
		}
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	initial := map[string]any{"type": "file-history-snapshot", "messageId": messageID, "snapshot": map[string]any{"messageId": snapshotID, "trackedFileBackups": map[string]any{}, "timestamp": now}, "isSnapshotUpdate": false}
	meta := map[string]any{"type": "session-meta", "isMeta": true, "workDir": workDir, "timestamp": now}
	if prepared.Repository != nil {
		meta["repository"] = prepared.Repository
	}
	if permissionMode != "" {
		meta["permissionMode"] = permissionMode
	}
	current := false
	if persist {
		current, err = server.shouldPersist(request.Context())
		if err != nil {
			return nil, err
		}
	}
	if !persist || !current {
		raw, _ := json.Marshal(meta)
		server.rememberMemory(sessionInfo{ID: id, Path: filepath.Join(directory, id+".jsonl"), ProjectPath: projectpath.Sanitize(workDir), WorkDir: workDir, PermissionMode: permissionMode, Metadata: raw, memory: true})
	}
	if !persist {
		server.rememberKnown(id)
		return map[string]string{"sessionId": id, "workDir": workDir}, nil
	}
	current, err = server.shouldPersist(request.Context())
	if err != nil {
		return nil, err
	}
	if !current {
		server.rememberKnown(id)
		return map[string]string{"sessionId": id, "workDir": workDir}, nil
	}
	file, err := os.OpenFile(filepath.Join(directory, id+".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(initial)
	if err == nil {
		err = encoder.Encode(meta)
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	server.rememberKnown(id)
	server.noteSessionMutation(filepath.Join(directory, id+".jsonl"))
	return map[string]string{"sessionId": id, "workDir": workDir}, nil
}

func (server *Server) scanSession(ctx context.Context, path string) (sessionInfo, error) {
	info := sessionInfo{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), ProjectPath: filepath.Base(filepath.Dir(path)), Path: path}
	projected, err := server.projection.Get(path, info.ProjectPath)
	if err != nil {
		return sessionInfo{}, err
	}
	if err = json.Unmarshal(projected.Summary, &info); err != nil {
		return sessionInfo{}, err
	}
	metadata := map[string]json.RawMessage{}
	// Preserve future session-meta fields while the shared projection owns
	// validated launch/summary fields and Source's title/EOF/budget policy.
	err = server.budget.With(ctx, sessionstore.MetadataLane, func() error {
		_, err := server.metadata.Stream(ctx, path, func(entry sessionmetadata.Entry, _ bool, _ int64) error {
			var row map[string]json.RawMessage
			if json.Unmarshal(entry.Value, &row) == nil && stringField(row, "type") == "session-meta" {
				for key, value := range row {
					metadata[key] = value
				}
			}
			return nil
		}, sessionmetadata.Options{})
		return err
	})
	if err != nil {
		return sessionInfo{}, err
	}
	var launch map[string]json.RawMessage
	if err = json.Unmarshal(projected.LaunchInfo, &launch); err != nil {
		return sessionInfo{}, err
	}
	for key, value := range launch {
		metadata[key] = value
	}
	info.Metadata, _ = json.Marshal(metadata)
	var summary map[string]json.RawMessage
	_ = json.Unmarshal(projected.Summary, &summary)
	if string(summary["isTeamWorker"]) == "true" {
		info.teamWorker = true
	}
	info.ProjectRoot = logicalProjectRootWith(info.WorkDir, summary, server.findCachedCanonicalGitRoot)
	info.WorkDirExists, info.WorkspaceState = sourceWorkspaceAvailability(info.WorkDir, info.ProjectRoot, summary)
	return info, nil
}

func (server *Server) allSessions(ctx context.Context) ([]sessionInfo, error) {
	limit := float64(2147483647)
	if rows, _, ok := server.indexedSessionList(ctx, localindex.SessionListOptions{Limit: &limit}); ok {
		return rows, nil
	}
	return server.allSessionsFromFiles(ctx)
}
func (server *Server) allSessionsFromFiles(ctx context.Context) ([]sessionInfo, error) {
	var result []sessionInfo
	root := filepath.Join(server.options.ConfigDir, "projects")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, fs.ErrNotExist) && path == root {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if path != root && filepath.Dir(path) != root {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Dir(filepath.Dir(path)) != root || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		info, err := server.scanSession(ctx, path)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		if !info.teamWorker {
			result = append(result, info)
		}
		return nil
	})
	if result == nil {
		result = []sessionInfo{}
	}
	sort.SliceStable(result, func(a, b int) bool {
		if result[a].ModifiedAt != result[b].ModifiedAt {
			return result[a].ModifiedAt > result[b].ModifiedAt
		}
		if result[a].ID != result[b].ID {
			return result[a].ID < result[b].ID
		}
		return result[a].Path < result[b].Path
	})
	return result, err
}

func (server *Server) findSession(ctx context.Context, id string) (sessionInfo, error) {
	if isSideChatID(id) {
		if info, ok := server.sideChatInfo(id); ok {
			return info, nil
		}
		return sessionInfo{}, notFound("Side chat expired")
	}
	if !sessionIDPattern.MatchString(id) {
		return sessionInfo{}, notFound("Session not found: " + id)
	}
	if found, ok := server.findIndexedSession(ctx, id); ok {
		return server.overlayMemory(found), nil
	}
	if found, ok := server.findSessionFromFiles(ctx, id); ok {
		return server.overlayMemory(found), nil
	}
	if info, ok := server.memorySession(id); ok {
		return info, nil
	}
	return sessionInfo{}, notFound("Session not found: " + id)
}

func (server *Server) messages(ctx context.Context, info sessionInfo, request *http.Request) (any, error) {
	query := request.URL.Query()
	mode := query.Get("mode")
	if mode != "" && mode != "full" && mode != "page" {
		return nil, badRequest("Invalid history mode")
	}
	if mode == "full" && query.Get("cursor") != "" {
		return nil, badRequest("mode=full does not take a cursor")
	}
	var limit *float64
	if value := query.Get("limit"); value != "" {
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, badRequest("Invalid limit parameter")
		}
		limit = &number
	}
	if info.memory {
		version := "memory"
		if info.ephemeral {
			version = "ephemeral"
		}
		return map[string]any{"messages": []json.RawMessage{}, "taskNotifications": []json.RawMessage{}, "page": map[string]any{"nextCursor": nil, "hasMore": false, "historyComplete": true, "sourceVersion": version, "scannedBytes": 0, "omittedOversizedEntries": 0}}, nil
	}
	page, err := server.reader.ReadPage(ctx, info.Path, sessionstore.PageOptions{Cursor: query.Get("cursor"), Limit: limit, Full: mode == "full"})
	if err != nil {
		return nil, err
	}
	messages, notifications := projectHistory(page.Entries)
	return map[string]any{"messages": messages, "taskNotifications": notifications, "page": page.Page}, nil
}

func (server *Server) sessionsHTTP(writer http.ResponseWriter, request *http.Request, parts []string) error {
	if len(parts) == 2 {
		switch request.Method {
		case http.MethodPost:
			result, err := server.createSession(request)
			if err == nil {
				server.invalidateRecentProjects()
				writeJSON(writer, 201, result)
			}
			return err
		case http.MethodGet:
			query := request.URL.Query()
			limit, offset := 20.0, 0.0
			for key, target := range map[string]*float64{"limit": &limit, "offset": &offset} {
				if value := query.Get(key); value != "" {
					prefix := parseIntPrefix.FindString(trimJS(value))
					number, err := strconv.ParseFloat(prefix, 64)
					if prefix == "" || err != nil && !math.IsInf(number, 0) || math.IsNaN(number) || number < 0 {
						return badRequest("Invalid " + key + " parameter")
					}
					*target = number
				}
			}

			if isPetRequest(request) {
				return server.petSessionListHTTP(writer, request, limit)
			}

			if query.Get("view") == "" && limit <= 9007199254740991 && offset <= 9007199254740991 {
				var project *string
				if value := query.Get("project"); value != "" {
					value = projectpath.Sanitize(value)
					project = &value
				}
				lim, off := float64(limit), float64(offset)
				if rows, total, ok := server.indexedSessionList(request.Context(), localindex.SessionListOptions{Project: project, Limit: &lim, Offset: &off}); ok {
					writeJSON(writer, 200, map[string]any{"sessions": rows, "total": total, "index": server.localIndexStatus()})
					return nil
				}
			}
			all, err := server.allSessions(request.Context())
			if err != nil {
				return err
			}
			filtered := []sessionInfo{}
			for _, info := range all {
				project := query.Get("project")
				if project == "" || project == info.WorkDir || project == info.ProjectPath {
					filtered = append(filtered, info)
				}
			}
			total := len(filtered)
			start := int(min(float64(total), offset))
			end := int(min(float64(total), float64(start)+limit))
			if view := query.Get("view"); view != "" {
				if view != "sidebar" {
					return badRequest("Invalid session list view")
				}
				return server.sidebarHTTP(writer, request, all)
			}
			writeJSON(writer, 200, map[string]any{"sessions": filtered[start:end], "total": total, "index": server.localIndexStatus()})
			return nil
		default:
			return methodNotAllowed(request.Method)
		}
	}
	id := parts[2]
	if len(parts) > 3 && parts[3] == "inspection" && request.Method != http.MethodGet {
		return methodNotAllowed(request.Method)
	}
	// Source recognizes history recovery only for GET. Other methods follow
	// the normal item route, including PATCH/DELETE, without starting recovery.
	if len(parts) > 3 && parts[3] == "history-recovery" && request.Method != http.MethodGet {
		if request.Method == http.MethodPatch || request.Method == http.MethodDelete {
			return server.sessionsHTTP(writer, request, parts[:3])
		}
		return methodNotAllowed(request.Method)
	}
	if len(parts) > 3 && parts[3] == "side-question" {
		return server.sideQuestionHTTP(writer, request, id, parts)
	}
	if len(parts) > 3 && parts[3] == "side-chats" {
		return server.sideChatsHTTP(writer, request, id, parts)
	}
	if len(parts) > 3 && parts[3] == "chat" {
		return server.conversationsHTTP(writer, request, parts)
	}
	if id == "recent-projects" {
		return server.recentProjectsHTTP(writer, request)
	}
	if id == "repository-context" && request.Method == http.MethodGet {
		return server.repositoryContextHTTP(writer, request)
	}
	if id == "repository-branch" {
		return server.createRepositoryBranchHTTP(writer, request)
	}
	if id == "project-history" {
		return server.projectHistoryHTTP(writer, request)
	}
	if id == "batch-delete" {
		return server.batchDeleteSessionsHTTP(writer, request)
	}
	if len(parts) > 3 && parts[3] == "trace" {
		return server.sessionTraceHTTP(writer, request, parts, server.options.Trace)
	}
	if len(parts) > 3 && parts[3] == "branch" {
		return server.branchSessionHTTP(writer, request, id)
	}
	if len(parts) > 3 && parts[3] == "slash-commands" && request.Method != http.MethodGet {
		return methodNotAllowed(request.Method)
	}
	info, err := server.findSession(request.Context(), id)
	if err != nil {
		return err
	}
	if len(parts) > 3 {
		switch parts[3] {
		case "slash-commands":
			return server.sessionSlashCommandsHTTP(writer, request, info)
		case "subagents":
			return server.subagentsHTTP(writer, request, info, parts)
		case "workspace":
			return server.workspaceHTTP(writer, request, info, parts)
		case "turn-checkpoints":
			return server.turnCheckpointsHTTP(writer, request, info, parts)
		case "rewind":
			return server.rewindHTTP(writer, request, info)
		case "git-info":
			return server.sessionGitInfoHTTP(writer, request, info)
		case "summary":
			if request.Method != http.MethodGet {
				return methodNotAllowed(request.Method)
			}
			if info.ephemeral {
				writeJSON(writer, 200, info)
				return nil
			}
			if info.memory {
				return notFound("Session not found: " + id)
			}
			info, err = server.scanSession(request.Context(), info.Path)
			if err != nil {
				return err
			}
			writeJSON(writer, 200, info)
			return nil
		case "messages":
			if request.Method != http.MethodGet {
				return methodNotAllowed(request.Method)
			}
			result, err := server.messages(requestSignalContext(request), info, request)
			if err == nil {
				writeJSON(writer, 200, result)
			}
			return err
		case "history-recovery":
			return server.historyRecoveryHTTP(writer, request, info)
		case "inspection":
			return server.inspectionHTTP(writer, request, info)
		default:
			return unsupported(strings.Join(parts[1:], "/"))
		}
	}
	switch request.Method {
	case http.MethodGet:
		if info.memory && !info.ephemeral {
			return notFound("Session not found: " + id)
		}
		if !info.ephemeral {
			info, err = server.scanSession(request.Context(), info.Path)
			if err != nil {
				return err
			}
		}
		result, err := server.messages(request.Context(), info, request)
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(info)
		var object map[string]any
		_ = json.Unmarshal(encoded, &object)
		for key, value := range result.(map[string]any) {
			object[key] = value
		}
		writeJSON(writer, 200, object)
		return nil
	case http.MethodDelete:
		return server.deleteSessionHTTP(writer, request, id)
	case http.MethodPatch:
		body, err := jsonObject(request)
		if err != nil {
			return err
		}
		title := stringField(body, "title")
		if title == "" {
			return badRequest("title (string) is required in request body")
		}
		persist, err := server.shouldPersist(request.Context())
		if err != nil {
			return err
		}
		current := false
		if persist {
			current, err = server.shouldPersist(request.Context())
			if err != nil {
				return err
			}
		}
		if !persist || !current || server.hasMemoryLaunch(id) {
			server.rememberMemoryTitle(info, title)
		}
		if !persist {
			server.rememberPrivateTitle(id, title)
			writeJSON(writer, 200, map[string]bool{"ok": true})
			return nil
		}
		current, err = server.shouldPersist(request.Context())
		if err != nil {
			return err
		}
		if !current {
			server.rememberPrivateTitle(id, title)
			writeJSON(writer, 200, map[string]bool{"ok": true})
			return nil
		}
		if info.memory || !server.canPersistTitle(id, title) {
			writeJSON(writer, 200, map[string]bool{"ok": true})
			return nil
		}
		file, err := os.OpenFile(info.Path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(map[string]string{"type": "custom-title", "customTitle": title, "sessionId": id})
		_, err = file.Write(append(encoded, '\n'))
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		server.noteSessionMutation(info.Path)
		if server.options.LocalIndex != nil {
			_, _ = server.options.LocalIndex.UpdateTitle(request.Context(), id, title)
		}
		writeJSON(writer, 200, map[string]bool{"ok": true})
		return nil
	default:
		return methodNotAllowed(request.Method)
	}
}
