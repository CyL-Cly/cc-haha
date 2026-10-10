package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/slashcommandsapi"
)

type inspectionControlSession interface {
	RequestInspectionControl(context.Context, protocol.JSONObject, time.Duration) (json.RawMessage, error)
}
type inspectionControlResult struct {
	value json.RawMessage
	err   error
}

func inspectionArray(raw []byte) bool { return len(raw) > 0 && raw[0] == '[' }
func inspectionObjectSpread(raw json.RawMessage) protocol.JSONObject {
	value := collaborationObject(raw)
	if value.IsObject() {
		return value
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		value = protocol.NewJSONObject()
		for i, item := range items {
			value.Set(strconv.Itoa(i), item)
		}
	}
	return value
}
func inspectionTokenTotal(value protocol.JSONObject) float64 {
	total := 0.0
	for _, key := range []string{"totalInputTokens", "totalOutputTokens", "totalCacheReadInputTokens", "totalCacheCreationInputTokens"} {
		total += inspectionNumber(value.Get(key))
	}
	return total
}

func (s *Server) inspectionHTTP(w http.ResponseWriter, r *http.Request, info sessionInfo) error {
	if r.Method != http.MethodGet {
		return methodNotAllowed(r.Method)
	}
	query := r.URL.Query()
	includeContext := query.Get("includeContext") != "0"
	contextOnly := includeContext && query.Get("contextOnly") == "1"
	usageOnly := !includeContext && query.Get("usageOnly") == "1"
	s.mu.Lock()
	current := s.runtimes[info.ID]
	s.mu.Unlock()
	var live LiveSession
	var options SessionOptions
	if current != nil {
		current.mu.Lock()
		if !current.retired && current.ctx.Err() == nil && current.closing == nil {
			live = current.live
			options = current.options
		}
		current.mu.Unlock()
	}
	active := live != nil
	var snapshot *inspectionTranscriptSnapshot
	loaded := false
	getSnapshot := func() (*inspectionTranscriptSnapshot, error) {
		if loaded {
			return snapshot, nil
		}
		var err error
		snapshot, err = s.getInspectionTranscript(r.Context(), info)
		loaded = err == nil
		return snapshot, err
	}
	workDir := ""
	mode := "default"
	if active {
		workDir = options.WorkDir
		mode = options.PermissionMode
		if source, ok := live.(CollaborationSession); ok {
			mode = source.CollaborationRuntime().String("permissionMode")
		}
	}
	if workDir == "" {
		value, err := getSnapshot()
		if err != nil {
			return err
		}
		if value != nil {
			workDir = value.LaunchInfo.String("workDir")
		}
	}
	if workDir == "" {
		return s.inspectionMissing(info.ID)
	}
	if !active {
		value, err := getSnapshot()
		if err != nil {
			return err
		}
		if value != nil && value.LaunchInfo.Get("permissionMode") != nil {
			mode = value.LaunchInfo.String("permissionMode")
		}
	}
	if mode == "" {
		mode = "default"
	}
	init := collaborationObject(s.sdkCapture(info.ID).Init())
	metadata := protocol.NewJSONObject()
	if !usageOnly && (!active || !init.IsObject()) {
		value, err := getSnapshot()
		if err != nil {
			return err
		}
		if value != nil {
			metadata = value.Metadata
		}
	}
	commands := []slashcommandsapi.Command{}
	if !usageOnly {
		if s.options.SkillSlashCommands == nil {
			return unsupported("inspection actual skill slash commands")
		}
		preferred := s.SessionSlashCommands(info.ID)
		fallback, err := s.options.SkillSlashCommands(r.Context(), workDir, len(preferred) == 0)
		if err != nil {
			return err
		}
		commands = fallback
		if len(preferred) > 0 {
			commands = slashcommandsapi.Merge(preferred, fallback)
		}
	}
	status := protocol.NewJSONObject()
	status.Set("sessionId", jsonString(info.ID))
	status.Set("workDir", jsonString(workDir))
	status.Set("permissionMode", jsonString(mode))
	for _, pair := range [][2]string{{"claude_code_version", "version"}, {"cwd", "cwd"}, {"model", "model"}} {
		raw := init.Get(pair[0])
		if !inspectionIsString(raw) {
			raw = metadata.Get(pair[1])
		}
		if raw != nil {
			status.Set(pair[1], raw)
		}
	}
	if status.Get("cwd") == nil {
		status.Set("cwd", jsonString(workDir))
	}
	for _, pair := range [][2]string{{"apiKeySource", "apiKeySource"}, {"output_style", "outputStyle"}} {
		if raw := init.Get(pair[0]); inspectionIsString(raw) {
			status.Set(pair[1], raw)
		}
	}
	for _, pair := range [][2]string{{"tools", "tools"}, {"mcp_servers", "mcpServers"}} {
		raw := init.Get(pair[0])
		if !inspectionArray(raw) {
			raw = []byte("[]")
		}
		status.Set(pair[1], raw)
	}
	count := len(commands)
	if raw := init.Get("slash_commands"); inspectionArray(raw) {
		var values []any
		_ = json.Unmarshal(raw, &values)
		count = len(values)
	}
	status.Set("slashCommandCount", recoveryJSON(count))
	skills := 0
	if raw := init.Get("skills"); inspectionArray(raw) {
		var values []any
		_ = json.Unmarshal(raw, &values)
		skills = len(values)
	}
	status.Set("skillCount", recoveryJSON(skills))
	response := protocol.NewJSONObject()
	response.Set("active", recoveryJSON(active))
	response.Set("status", recoveryJSON(status))
	failures := protocol.NewJSONObject()
	if !active {
		value, err := getSnapshot()
		if err != nil {
			return err
		}
		if value != nil {
			if sourceJSONTruthy(value.ContextEstimate) {
				response.Set("contextEstimate", value.ContextEstimate)
			}
			if sourceJSONTruthy(value.Usage) {
				response.Set("usage", value.Usage)
			}
		}
		if response.Get("usage") == nil {
			failures.Set("usage", jsonString("CLI session is not running"))
		}
		if includeContext {
			failures.Set("context", jsonString("CLI session is not running"))
		}
		response.Set("errors", recoveryJSON(failures))
		writeJSON(w, 200, response)
		return nil
	}
	control := func(subtype string, timeout time.Duration) inspectionControlResult {
		input := protocol.NewJSONObject()
		input.Set("subtype", jsonString(subtype))
		if subtype == "get_context_usage" {
			input.Set("estimateOnly", []byte("true"))
		}
		consumer, ok := live.(inspectionControlSession)
		if !ok {
			return inspectionControlResult{err: errors.New("CLI session is not running")}
		}
		value, err := consumer.RequestInspectionControl(requestSignalContext(r), input, timeout)
		return inspectionControlResult{value, err}
	}
	if usageOnly {
		result := control("get_session_usage", 2500*time.Millisecond)
		if requestSignalContext(r).Err() != nil {
			return context.Cause(requestSignalContext(r))
		}
		if result.err != nil {
			failures.Set("usage", jsonString(result.err.Error()))
		} else {
			usage := inspectionObjectSpread(result.value)
			usage.Set("source", jsonString("current_process"))
			response.Set("usage", recoveryJSON(usage))
		}
	} else if contextOnly {
		result := control("get_context_usage", 20*time.Second)
		if requestSignalContext(r).Err() != nil {
			return context.Cause(requestSignalContext(r))
		}
		if result.err != nil {
			failures.Set("context", jsonString(result.err.Error()))
		} else {
			response.Set("context", result.value)
		}
		if response.Get("context") == nil {
			value, err := getSnapshot()
			if err != nil {
				return err
			}
			if value != nil && sourceJSONTruthy(value.ContextEstimate) {
				response.Set("contextEstimate", value.ContextEstimate)
			}
		}
	} else {
		basic := 4 * time.Second
		if includeContext {
			basic = 10 * time.Second
		}
		usageCh, mcpCh := make(chan inspectionControlResult, 1), make(chan inspectionControlResult, 1)
		go func() { usageCh <- control("get_session_usage", basic) }()
		go func() { mcpCh <- control("mcp_status", basic) }()
		contextCh := make(chan inspectionControlResult, 1)
		if includeContext {
			go func() { contextCh <- control("get_context_usage", 20*time.Second) }()
		} else {
			contextCh <- inspectionControlResult{}
		}
		usage, contextResult, mcp := <-usageCh, <-contextCh, <-mcpCh
		if requestSignalContext(r).Err() != nil {
			return context.Cause(requestSignalContext(r))
		}
		value, err := getSnapshot()
		if err != nil {
			return err
		}
		if usage.err == nil {
			liveUsage := inspectionObjectSpread(usage.value)
			liveUsage.Set("source", jsonString("current_process"))
			if value != nil && sourceJSONTruthy(value.Usage) && inspectionTokenTotal(collaborationObject(value.Usage)) > inspectionTokenTotal(liveUsage) {
				response.Set("usage", value.Usage)
			} else {
				response.Set("usage", recoveryJSON(liveUsage))
			}
		} else if value != nil && sourceJSONTruthy(value.Usage) {
			response.Set("usage", value.Usage)
		} else {
			failures.Set("usage", jsonString(usage.err.Error()))
		}
		if includeContext {
			if contextResult.err == nil && sourceJSONTruthy(contextResult.value) {
				response.Set("context", contextResult.value)
			} else {
				message := "undefined"
				if contextResult.err != nil {
					message = contextResult.err.Error()
				}
				failures.Set("context", jsonString(message))
				if value != nil && sourceJSONTruthy(value.ContextEstimate) {
					response.Set("contextEstimate", value.ContextEstimate)
				}
			}
		}
		if mcp.err == nil && inspectionArray(collaborationObject(mcp.value).Get("mcpServers")) {
			status.Set("mcpServers", collaborationObject(mcp.value).Get("mcpServers"))
			response.Set("status", recoveryJSON(status))
		}
	}
	response.Set("errors", recoveryJSON(failures))
	writeJSON(w, 200, response)
	return nil
}
