package clibootstrap

import (
	"encoding/json"
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
)

type nativeStatusLineProjection struct {
	State    TerminalStatusLineState
	Settings protocol.JSONObject
	Hook     protocol.JSONObject
	History  []protocol.JSONObject
	Disabled bool
}

func (m *MainPrint) nativeStatusLineProjection(scope *cooperative.Scope) (nativeStatusLineProjection, error) {
	var result nativeStatusLineProjection
	if m == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || !m.options.Interactive || m.agentChild || !m.initialized || m.Host == nil {
		return result, cooperative.ErrRestricted
	}
	if m.closed {
		return result, cooperative.ErrClosed
	}
	settings, permission, err := m.Controller.SDKSettingsOnOwner(scope)
	if err != nil {
		return result, err
	}
	result.Settings = protocol.NewJSONObject()
	if settings != nil {
		result.Settings = settings.Clone()
	}
	configured, ok := result.Settings.Object("statusLine")
	result.State.Configured = result.Settings.Get("statusLine") != nil
	if ok {
		result.State.Command = configured.String("command")
		_ = json.Unmarshal(configured.Get("padding"), &result.State.Padding)
	}
	result.State.PermissionMode = permission.String("mode")
	// useMainLoopModel samples AppState, including its session override;
	// the last ASK's resolved inference model can remain stale while idle.
	typed, err := m.Controller.TypedModelStateOnOwner(scope)
	if err != nil {
		return result, err
	}
	selected := m.nativeModelSelector.DefaultSetting()
	if typed.ForSession != nil {
		selected = quoteMain(*typed.ForSession)
	} else if typed.Base != nil {
		selected = quoteMain(*typed.Base)
	}
	parsed, err := m.nativeModelSelector.Parse(selected)
	if err != nil {
		return result, err
	}
	result.State.Model, err = decodeNativeModel(parsed)
	if err != nil {
		return result, err
	}
	result.History, err = querypreflight.InspectionRecords(m.Host.History())
	if err != nil {
		return result, err
	}
	result.State.AssistantID = "null"
	for i := len(result.History) - 1; i >= 0; i-- {
		message := result.History[i]
		if message.String("type") != "assistant" {
			continue
		}
		if units, err := protocol.DecodeJavaScriptJSONString(message.Get("uuid")); err == nil {
			result.State.AssistantID = string(protocol.QuoteJavaScriptUTF16(units))
		}
		break
	}
	result.State.Trusted, err = m.nativeGoalTrust(scope)
	if err != nil {
		return result, err
	}
	global, err := m.Global.Get()
	if err != nil {
		return result, err
	}
	if global != nil {
		result.State.EditorMode = global.String("editorMode")
	}
	policy, err := m.Startup.GetSettingsForSource("policySettings")
	if err != nil {
		return result, err
	}
	result.State.UserHooksDisabled = string(result.Settings.Get("disableAllHooks")) == "true"
	result.Hook = configured
	if policy != nil {
		result.Disabled = string(policy.Get("disableAllHooks")) == "true"
	}
	if result.State.UserHooksDisabled || policy != nil && string(policy.Get("allowManagedHooksOnly")) == "true" {
		result.Hook = protocol.NewJSONObject()
		if policy != nil {
			result.Hook, _ = policy.Object("statusLine")
		}
	}
	return result, nil
}

// Source caches the 200k observation by the actual last assistant UUID. This
// cache is carried by one mounted component, independently of ASK and tools.
func (m *MainPrint) nativeStatusLineInput(scope *cooperative.Scope, projection nativeStatusLineProjection, vimMode string, cache TerminalStatusLineCache) (TerminalStatusLineCache, protocol.JSONObject, error) {
	empty := protocol.JSONObject{}
	if projection.State.AssistantID != cache.AssistantID {
		value, err := preflightcore.DoesMostRecentAssistantMessageExceed200k(projection.History)
		if err != nil {
			return cache, empty, err
		}
		cache.AssistantID, cache.Exceeds200k = projection.State.AssistantID, value
	}
	rawModel, err := m.nativeModelSelector.RuntimeModel(quoteMain(projection.State.Model), projection.State.PermissionMode, cache.Exceeds200k)
	if err != nil {
		return cache, empty, err
	}
	model, err := decodeNativeModel(rawModel)
	if err != nil {
		return cache, empty, err
	}
	label, err := m.nativeModelSelector.PublicModelDisplayName(rawModel)
	if err != nil {
		return cache, empty, err
	}
	usage, err := m.Host.NativeSessionUsage(scope)
	if err != nil {
		return cache, empty, err
	}
	_, permission, err := m.Controller.SDKSettingsOnOwner(scope)
	if err != nil {
		return cache, empty, err
	}
	directories := []json.RawMessage{}
	for _, directory := range sdkpermissions.WorkspaceDirectoryEntries(permission) {
		directories = append(directories, directory.Get("path"))
	}
	resolver := modelaccounting.RestoredModelResolver{Environment: m.nativeOpenAIEnvironment(), Settings: m.Startup.GetInitialSettings, Global: m.Global.Get, SDKBetas: m.nativeSDKBetas}
	window, err := resolver.ContextWindow(model)
	if err != nil {
		return cache, empty, err
	}
	plan, err := m.nativePlanProjection(scope)
	if err != nil {
		return cache, empty, err
	}
	estimated, err := preflightcore.EstimateContextWithAttachments(projection.History, &preflightcore.AttachmentOptions{Factory: m.factory, Plan: plan})
	if err != nil {
		return cache, empty, err
	}
	current, err := preflightcore.CurrentContextUsage(projection.History)
	if err != nil {
		return cache, empty, err
	}
	tokens := math.Min(estimated, window)
	if current != nil {
		count := 0.0
		for _, field := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
			var n float64
			_ = json.Unmarshal(current.Get(field), &n)
			count += n
		}
		environment := m.nativeOpenAIEnvironment()
		lowTrust := !nativeGlobalCacheScope(environment) || toolsearch.Provider(environment) != "firstParty"
		ignore := lowTrust && statusLineHasMediaInput(projection.History) && estimated > 0 && count >= window && estimated < window && count > math.Max(estimated*4, estimated+50000)
		if !ignore {
			tokens = math.Min(math.Max(estimated, count), window)
		}
	}
	percent := math.Min(100, math.Max(0, math.Floor(tokens/window*100+0.5)))
	used, remaining := rawMain(percent), rawMain(100-percent)
	if window <= 0 {
		used, remaining = json.RawMessage("null"), json.RawMessage("null")
	}
	metadata, err := m.Controller.TerminalMetadataOnOwner(scope)
	if err != nil {
		return cache, empty, err
	}
	transcript, err := m.Controller.Writer().ComputedTranscriptPath(scope)
	if err != nil {
		return cache, empty, err
	}
	state := m.state.Snapshot()
	style := projection.Settings.Get("outputStyle")
	if !nativeSettingsTruthy(style) {
		style = quoteMain("default")
	}
	result := protocol.NewJSONObject(fieldMain("session_id", state.SessionID), fieldMain("transcript_path", transcript), fieldMain("cwd", state.CWD), fieldMain("model", protocol.NewJSONObject(fieldMain("id", model), fieldMain("display_name", label))), fieldMain("workspace", protocol.NewJSONObject(fieldMain("current_dir", state.CWD), fieldMain("project_dir", state.OriginalCWD), fieldMain("added_dirs", directories))), fieldMain("version", m.options.Version), fieldMain("output_style", protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: style})), fieldMain("exceeds_200k_tokens", cache.Exceeds200k))
	cost := protocol.NewJSONObject()
	for _, pair := range [][2]string{{"total_cost_usd", "totalCostUSD"}, {"total_duration_ms", "totalDuration"}, {"total_api_duration_ms", "totalAPIDuration"}, {"total_lines_added", "totalLinesAdded"}, {"total_lines_removed", "totalLinesRemoved"}} {
		cost.Set(pair[0], usage.Get(pair[1]))
	}
	result.Set("cost", rawMain(cost))
	result.Set("context_window", rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "total_input_tokens", Value: usage.Get("totalInputTokens")}, protocol.JSONField{Name: "total_output_tokens", Value: usage.Get("totalOutputTokens")}, fieldMain("context_window_size", window), fieldMain("current_usage", current), protocol.JSONField{Name: "used_percentage", Value: used}, protocol.JSONField{Name: "remaining_percentage", Value: remaining})))
	m.nativeTerminalStatusLine.mu.Lock()
	rates := m.nativeTerminalStatusLine.rates.Clone()
	m.nativeTerminalStatusLine.mu.Unlock()
	if len(rates.OwnProperties()) > 0 {
		result.Set("rate_limits", rawMain(rates))
	}
	if name := metadata.Get("customTitle"); nativeSettingsTruthy(name) {
		result.Set("session_name", name)
	}
	global, err := m.Global.Get()
	if err != nil {
		return cache, empty, err
	}
	if global != nil && global.String("editorMode") == "vim" {
		if vimMode == "" {
			vimMode = "INSERT"
		}
		result.Set("vim", rawMain(map[string]string{"mode": vimMode}))
	}
	if agent := m.nativeMainThreadAgentType(); agent != "" {
		result.Set("agent", rawMain(map[string]string{"name": agent}))
		result.Set("agent_type", quoteMain(agent))
	}
	if m.Controller.NativeRemoteFrontend() || m.options.Arguments.SDKURL != "" {
		result.Set("remote", rawMain(map[string]string{"session_id": state.SessionID}))
	}
	if m.worktreeState != nil && m.worktreeState.current != nil {
		worktree := m.worktreeState.current
		value := protocol.NewJSONObject(fieldMain("name", worktree.Name), fieldMain("path", worktree.Path), fieldMain("original_cwd", worktree.OriginalCWD))
		if worktree.Branch != "" {
			value.Set("branch", quoteMain(worktree.Branch))
		}
		if worktree.OriginalBranch != "" {
			value.Set("original_branch", quoteMain(worktree.OriginalBranch))
		}
		result.Set("worktree", rawMain(value))
	}
	return cache, result, nil
}

func statusLineHasMediaInput(history []protocol.JSONObject) bool {
	for _, record := range history {
		if record.String("type") == "attachment" {
			attachment, _ := record.Object("attachment")
			content, _ := attachment.Object("content")
			if attachment.String("type") == "file" && (content.String("type") == "image" || content.String("type") == "pdf") {
				return true
			}
		}
		if record.String("type") != "user" && record.String("type") != "assistant" {
			continue
		}
		message, _ := record.Object("message")
		var content []protocol.JSONObject
		_ = json.Unmarshal(message.Get("content"), &content)
		for _, block := range content {
			if block.String("type") == "image" || block.String("type") == "document" {
				return true
			}
		}
	}
	return false
}
