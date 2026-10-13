package server

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

func (server *Server) shouldPersist(ctx context.Context) (bool, error) {
	if server.options.PersistSession == nil {
		return true, nil
	}
	return server.options.PersistSession(ctx)
}

func cloneSessionInfo(info sessionInfo) sessionInfo {
	info.Metadata = append(json.RawMessage(nil), info.Metadata...)
	info.RuntimeProviderID = append(json.RawMessage(nil), info.RuntimeProviderID...)
	return info
}

func (server *Server) rememberKnown(id string) {
	server.mu.Lock()
	server.knownSessions[id] = true
	server.mu.Unlock()
}
func (server *Server) rememberMemory(info sessionInfo) {
	server.mu.Lock()
	server.memoryLaunch[info.ID] = cloneSessionInfo(info)
	server.mu.Unlock()
}
func (server *Server) hasMemoryLaunch(id string) bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	_, ok := server.memoryLaunch[id]
	return ok
}
func (server *Server) memorySession(id string) (sessionInfo, bool) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if info, ok := server.memoryLaunch[id]; ok {
		info = cloneSessionInfo(info)
		info.memory = true
		info.MessageCount = 0
		return info, true
	}
	if server.knownSessions[id] {
		return sessionInfo{ID: id, WorkDir: server.options.WorkDir, memory: true}, true
	}
	return sessionInfo{}, false
}

func mergeLaunch(info sessionInfo, raw json.RawMessage) (sessionInfo, error) {
	metadata := map[string]json.RawMessage{}
	if len(info.Metadata) > 0 {
		if err := json.Unmarshal(info.Metadata, &metadata); err != nil {
			return info, err
		}
	}
	if metadata == nil {
		metadata = map[string]json.RawMessage{}
	}
	var next map[string]json.RawMessage
	if err := json.Unmarshal(raw, &next); err != nil {
		return info, err
	}
	for key, value := range next {
		metadata[key] = append(json.RawMessage(nil), value...)
	}
	if workDir := stringField(metadata, "workDir"); workDir != "" {
		info.WorkDir = workDir
		info.ProjectPath = projectpath.Sanitize(workDir)
	}
	if mode := stringField(metadata, "permissionMode"); mode != "" {
		info.PermissionMode = mode
	}
	if title := stringField(metadata, "customTitle"); title != "" {
		info.Title = title
	}
	if value, ok := metadata["runtimeProviderId"]; ok {
		info.RuntimeProviderID = append(json.RawMessage(nil), value...)
	}
	if model := stringField(metadata, "runtimeModelId"); model != "" {
		info.RuntimeModelID = model
	}
	var record protocol.JSONObject
	if err := record.UnmarshalJSON(raw); err != nil {
		return info, err
	}
	var previous json.RawMessage
	if info.EffortLevel != "" {
		previous, _ = json.Marshal(info.EffortLevel)
	}
	effort := sessionprojection.ResolveSessionEffortLevel(record, previous)
	if len(effort) > 0 {
		metadata["effortLevel"] = effort
		_ = json.Unmarshal(effort, &info.EffortLevel)
	} else {
		delete(metadata, "effortLevel")
		info.EffortLevel = ""
	}
	if fast := record.Get("openAIFastMode"); string(fast) == "true" || string(fast) == "false" {
		var enabled bool
		_ = json.Unmarshal(fast, &enabled)
		info.OpenAIFastMode = &enabled
		metadata["openAIFastMode"] = fast
	}
	info.Metadata, _ = json.Marshal(metadata)
	return info, nil
}

func (server *Server) overlayMemory(info sessionInfo) sessionInfo {
	server.mu.Lock()
	memory, ok := server.memoryLaunch[info.ID]
	server.mu.Unlock()
	if !ok {
		return info
	}
	merged, err := mergeLaunch(info, memory.Metadata)
	if err != nil {
		return info
	}
	var metadata map[string]json.RawMessage
	_ = json.Unmarshal(merged.Metadata, &metadata)
	var original map[string]json.RawMessage
	_ = json.Unmarshal(info.Metadata, &original)
	if count, exists := original["transcriptMessageCount"]; exists {
		metadata["transcriptMessageCount"] = append(json.RawMessage(nil), count...)
	} else {
		metadata["transcriptMessageCount"], _ = json.Marshal(info.MessageCount)
	}
	merged.Metadata, _ = json.Marshal(metadata)
	merged.memory = false
	return merged
}

// The shared application calls this only after its real runtime transition has
// succeeded. It stores Source launch state when retention removed or disabled
// the transcript; the existing Controller remains the disk writer.
func (server *Server) updateLaunchMetadata(ctx context.Context, id string, raw json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if isSideChatID(id) {
		server.mu.Lock()
		defer server.mu.Unlock()
		side := server.sideChats[id]
		if side == nil || side.closed {
			return nil
		}
		next, e := mergeLaunch(side.info, raw)
		if e != nil {
			return e
		}
		next.memory = true
		next.ephemeral = true
		side.info = next
		return nil
	}
	info, err := server.findSession(ctx, id)
	if err != nil {
		return err
	}
	persist, err := server.shouldPersist(ctx)
	if err != nil {
		return err
	}
	current := false
	if persist {
		current, err = server.shouldPersist(ctx)
		if err != nil {
			return err
		}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.closed || server.deleting[id] {
		return context.Canceled
	}
	server.knownSessions[id] = true
	_, retained := server.memoryLaunch[id]
	if persist && current && !retained {
		return nil
	}
	if previous, ok := server.memoryLaunch[id]; ok {
		info = cloneSessionInfo(previous)
	}
	next, err := mergeLaunch(info, raw)
	if err != nil {
		return err
	}
	next.Path = filepath.Join(server.options.ConfigDir, "projects", projectpath.Sanitize(next.WorkDir), id+".jsonl")
	next.memory = true
	server.memoryLaunch[id] = next
	if !persist || !current {
		var metadata map[string]json.RawMessage
		_ = json.Unmarshal(raw, &metadata)
		if title := stringField(metadata, "customTitle"); title != "" {
			server.privateTitleLocked(id, title)
		}
	}
	return nil
}

func (server *Server) privateTitleLocked(id, title string) {
	if server.privateTitles[id] == nil {
		server.privateTitles[id] = map[string]bool{}
	}
	server.privateTitles[id][title] = true
}
func (server *Server) rememberPrivateTitle(id, title string) {
	server.mu.Lock()
	server.privateTitleLocked(id, title)
	server.mu.Unlock()
}
func (server *Server) canPersistTitle(id, title string) bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	return !server.privateTitles[id][title]
}
func (server *Server) rememberMemoryTitle(info sessionInfo, title string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if previous, ok := server.memoryLaunch[info.ID]; ok {
		info = cloneSessionInfo(previous)
	}
	raw, _ := json.Marshal(map[string]string{"customTitle": title})
	if next, err := mergeLaunch(info, raw); err == nil {
		server.memoryLaunch[info.ID] = next
	}
}
