// Package querypreflight is an ignored finite production-service candidate.
package querypreflight

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"math"
	"strconv"
	"strings"
	"sync"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "preflight service capability not migrated: " + e.Boundary
}
func capError(s string) error { return &CapabilityError{s} }

type InitialSettingsGetter interface {
	GetInitialSettings() (*protocol.JSONObject, error)
}
type Dependencies struct {
	Global          *cliglobalconfig.Service
	InitialSettings InitialSettingsGetter
	Factory         *messages.Factory
}
type Input struct {
	NativeClaudeProof                          *cooperative.NativeClaudeRequest
	PlanOptions                                *attachments.PlanOptions
	Profile                                    string
	History                                    []queryloop.Message
	SystemPrompt                               []json.RawMessage
	UserContext, SystemContext                 protocol.JSONObject
	MainLoopModel, QuerySource, PermissionMode string
	MainLoopModelOverride                      json.RawMessage // nil is undefined; null is explicit.
	OriginalEnvironment                        map[string]string
	ConsecutiveFailures                        *float64
	ToolCount                                  int
	Tools                                      []toolschema.Definition
	ContentReplacementStatePresent             bool
	CurrentPermissionMode                      func() (string, error)
	ResolveRuntimeModel                        func(RuntimeModelInput) (string, error)
}

// RuntimeModelInput belongs to the actual query setup after autocompact. The
// configured main model remains the context-budget authority; CurrentModel is
// a separate sampling and protected-signature decision.
type RuntimeModelInput struct {
	MainLoopModel     string
	PermissionMode    string
	Exceeds200kTokens bool
}
type Statistics struct {
	BoundaryIndex                                       int `json:"boundaryIndex"`
	WarningBefore, WarningAfter                         bool
	MicroConfigEvaluations, GlobalGets, SettingsGets    int
	FixedPrefixEstimate, AutoEstimate, BlockingEstimate float64
	AutoChecked                                         bool
}
type Result struct {
	ModelHistory, ToolContextHistory []queryloop.Message
	SystemPrompt                     []json.RawMessage
	CurrentModel                     string
	Blocked                          *queryloop.Message
	Statistics                       Statistics
}

// Service warning ownership is the process/runtime lifetime, not one request.
// Source's remote config/client are unavailable in this external release.
type Service struct {
	dep     Dependencies
	mu      sync.Mutex
	warning bool
}

func New(dep Dependencies) (*Service, error) {
	if dep.Global == nil || dep.InitialSettings == nil || dep.Factory == nil {
		return nil, errors.New("preflight requires real configuration services and shared factory")
	}
	return &Service{dep: dep}, nil
}
func (s *Service) SuppressWarning()        { s.mu.Lock(); defer s.mu.Unlock(); s.warning = true }
func (s *Service) WarningSuppressed() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.warning }
func (s *Service) Run(_ context.Context, in Input) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Result{CurrentModel: in.MainLoopModel, Statistics: Statistics{BoundaryIndex: -1, WarningBefore: s.warning}}
	if err := validate(in); err != nil {
		return out, err
	}
	original, err := records(in.History)
	if err != nil {
		return out, err
	}
	selected, err := preflightcore.AfterCompactBoundary(original)
	if err != nil {
		return out, err
	}
	current := mapProjection(in.History, selected)
	if len(selected.Records) > 0 && selected.Records[0].Record.String("type") == "system" && selected.Records[0].Record.String("subtype") == "compact_boundary" {
		out.Statistics.BoundaryIndex = selected.Records[0].OriginalIndex
	}
	rs := projectionRecords(selected)
	// Source applyToolResultBudget has a real early return for absent state.
	// microcompactMessages clears this BEFORE evaluating default time config.
	s.warning = false
	out.Statistics.WarningAfter = false
	out.Statistics.MicroConfigEvaluations++
	// External getFeatureValue: getEnvOverrides/configOverrides are ant-only,
	// then empty getGrowthBookClientKey makes default enabled:false authoritative.
	if in.OriginalEnvironment["USER_TYPE"] == "ant" {
		return out, capError("internal GrowthBook time microcompact overrides")
	}
	out.SystemPrompt, err = appendContext(in.SystemPrompt, in.SystemContext)
	if err != nil {
		return out, err
	}
	if !envTruthy(in.OriginalEnvironment["DISABLE_COMPACT"]) && (in.ConsecutiveFailures == nil || !(*in.ConsecutiveFailures >= 3)) {
		out.Statistics.FixedPrefixEstimate, err = fixedEstimate(in)
		if err != nil {
			return out, err
		}
		if in.QuerySource != "compact" && in.QuerySource != "session_memory" {
			enabled, e := s.autoEnabled(in, &out.Statistics)
			if e != nil {
				return out, e
			}
			if enabled {
				out.Statistics.AutoChecked = true
				usable, e := preflightcore.HasUsableContextUsage(rs)
				if e != nil {
					return out, e
				}
				count, e := preflightcore.EstimateContextWithAttachments(rs, &preflightcore.AttachmentOptions{Factory: s.dep.Factory, Plan: in.PlanOptions})
				if e != nil {
					return out, e
				}
				if !usable {
					count += out.Statistics.FixedPrefixEstimate
				}
				out.Statistics.AutoEstimate = count
				warning, e := s.warningState(count, in, &out.Statistics)
				if e != nil {
					return out, e
				}
				if warning.auto {
					return out, capError("active autocompact conversation/session-memory/hooks")
				}
			}
		}
	}
	// Preserve ToolContext before the model-local protected signature projection.
	out.ToolContextHistory = current
	if in.ResolveRuntimeModel != nil {
		mode := in.PermissionMode
		if in.CurrentPermissionMode != nil {
			mode, err = in.CurrentPermissionMode()
			if err != nil {
				return out, err
			}
		}
		if mode != "default" && mode != "bypassPermissions" && mode != "plan" && mode != "acceptEdits" && mode != "dontAsk" {
			return out, capError("native runtime permission mode " + mode)
		}
		input := RuntimeModelInput{MainLoopModel: in.MainLoopModel, PermissionMode: mode}
		if mode == "plan" {
			input.Exceeds200kTokens, err = preflightcore.DoesMostRecentAssistantMessageExceed200k(rs)
			if err != nil {
				return out, err
			}
		}
		out.CurrentModel, err = in.ResolveRuntimeModel(input)
		if err != nil {
			return out, err
		}
		if _, err = nativeModel(out.CurrentModel); err != nil {
			return out, err
		}
	} else {
		var specified json.RawMessage
		if specified, err = s.specifiedModel(in, &out.Statistics); err != nil {
			return out, err
		}
		if in.Profile == NativeProcessProfile && in.PermissionMode == "plan" && (string(specified) == `"opusplan"` || string(specified) == `"haiku"`) {
			return out, capError("native plan-mode alias model switch")
		}
		if _, err = s.specifiedModel(in, &out.Statistics); err != nil {
			return out, err
		}
	}
	modelProfile := ""
	if in.Profile == NativeProcessProfile {
		modelProfile = preflightcore.NativeDirectRelayProfile
	}
	projected, err := preflightcore.StripProtectedSignatures(rs, preflightcore.ModelInput{Profile: modelProfile, Original: quote(out.CurrentModel), Environment: modelEnvironment(in.OriginalEnvironment)})
	if err != nil {
		return out, err
	}
	out.ModelHistory = mapProjection(current, projected)
	if in.QuerySource != "compact" && in.QuerySource != "session_memory" {
		rs = projectionRecords(projected)
		out.Statistics.BlockingEstimate, err = preflightcore.EstimateContextWithAttachments(rs, &preflightcore.AttachmentOptions{Factory: s.dep.Factory, Plan: in.PlanOptions})
		if err != nil {
			return out, err
		}
		warning, e := s.warningState(out.Statistics.BlockingEstimate, in, &out.Statistics)
		if e != nil {
			return out, e
		}
		if warning.blocking {
			record, e := s.dep.Factory.CreateOrderedAssistantAPIError(storage.Object{"content": quote("Prompt is too long"), "error": quote("invalid_request"), "businessErrorCode": quote("prompt_too_long")})
			if e != nil {
				return out, e
			}
			out.Blocked = &queryloop.Message{Record: record}
		}
	}
	return out, nil
}
func (s *Service) autoEnabled(in Input, st *Statistics) (bool, error) {
	if envTruthy(in.OriginalEnvironment["DISABLE_COMPACT"]) || envTruthy(in.OriginalEnvironment["DISABLE_AUTO_COMPACT"]) {
		return false, nil
	}
	st.GlobalGets++
	cfg, err := s.dep.Global.Get()
	if err != nil {
		return false, err
	}
	return truthy(cfg.Get("autoCompactEnabled")), nil
}
func (s *Service) settings(st *Statistics) (*protocol.JSONObject, error) {
	st.SettingsGets++
	cfg, err := s.dep.InitialSettings.GetInitialSettings()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, errors.New("initial settings getter must return Source fallback object")
	}
	return cfg, nil
}
func (s *Service) specifiedModel(in Input, st *Statistics) (json.RawMessage, error) {
	value := in.MainLoopModelOverride
	if value == nil {
		cfg, err := s.settings(st)
		if err != nil {
			return nil, err
		}
		if in.OriginalEnvironment["ANTHROPIC_MODEL"] != "" {
			value = quote(in.OriginalEnvironment["ANTHROPIC_MODEL"])
		} else {
			value = cfg.Get("model")
		}
	}
	if truthy(value) {
		cfg, err := s.settings(st)
		if err != nil {
			return nil, err
		}
		if truthy(cfg.Get("availableModels")) {
			return nil, capError("active model allowlist resolver")
		}
	}
	return value, nil
}

type warningResult struct{ auto, blocking bool }

func (s *Service) warningState(count float64, in Input, st *Statistics) (warningResult, error) {
	effective, threshold := thresholds(in.OriginalEnvironment)
	if in.Profile == NativeProcessProfile {
		effective, threshold = nativeThresholds(in.MainLoopModel, in.OriginalEnvironment)
	}
	// calculateTokenWarningState deliberately evaluates global enabled twice.
	if _, err := s.autoEnabled(in, st); err != nil {
		return warningResult{}, err
	}
	enabled, err := s.autoEnabled(in, st)
	if err != nil {
		return warningResult{}, err
	}
	blocking := effective - 3000
	if v := parseInteger(in.OriginalEnvironment["CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE"]); v > 0 {
		blocking = v
	}
	return warningResult{enabled && count >= threshold, count >= blocking}, nil
}
func records(history []queryloop.Message) ([]protocol.JSONObject, error) {
	result := make([]protocol.JSONObject, 0, len(history))
	for _, item := range history {
		record := item.Record
		if item.Assistant != nil {
			record = item.Assistant.Record.Clone()
			raw, err := item.Assistant.MessageJSON()
			if err != nil {
				return nil, err
			}
			record.Set("message", raw)
		}
		if !record.IsObject() {
			return nil, capError("raw nonobject history")
		}
		result = append(result, record)
	}
	return result, nil
}
func mapProjection(history []queryloop.Message, p preflightcore.Projection) []queryloop.Message {
	output := make([]queryloop.Message, 0, len(p.Records))
	for _, r := range p.Records {
		if !r.EnvelopeChanged && !r.MessageChanged {
			output = append(output, history[r.OriginalIndex])
		} else {
			output = append(output, queryloop.Message{Record: r.Record})
		}
	}
	return output
}
func validate(in Input) error {
	if in.Profile != "" && in.Profile != NativeProcessProfile {
		return capError("unknown original preflight profile")
	}
	if in.Profile != NativeProcessProfile && (in.ResolveRuntimeModel != nil || in.CurrentPermissionMode != nil) {
		return capError("runtime model callback outside native process")
	}
	if in.CurrentPermissionMode != nil && in.ResolveRuntimeModel == nil {
		return capError("runtime permission callback without model authority")
	}
	if in.Profile == NativeProcessProfile {
		if _, err := nativeModel(in.MainLoopModel); err != nil {
			return err
		}
	} else if in.MainLoopModel != "fixture-model" && in.MainLoopModel != "deepseek-flash" {
		return capError("model context/output/alias resolver " + in.MainLoopModel)
	}
	if in.PermissionMode != "default" && in.PermissionMode != "bypassPermissions" && (in.Profile != NativeProcessProfile || in.PermissionMode != "plan" && in.PermissionMode != "acceptEdits" && (in.PermissionMode != "dontAsk" || in.ResolveRuntimeModel == nil)) {
		return capError("nondefault permission runtime model selection")
	}
	if in.QuerySource == "" {
		return capError("undefined querySource startsWith error")
	}
	if (in.ToolCount != 0 && in.ToolCount != len(in.Tools)) || in.ContentReplacementStatePresent {
		return capError("native tools/tool-result-budget host")
	}
	if !in.UserContext.IsObject() || !in.SystemContext.IsObject() {
		return capError("raw scalar user/system context")
	}
	for key := range in.OriginalEnvironment {
		if in.Profile == NativeProcessProfile && (key == "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS" || key == "CLAUDE_CODE_MAX_OUTPUT_TOKENS" || key == "CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS" || key == "ANTHROPIC_DEFAULT_FABLE_MODEL") {
			continue
		}
		switch key {
		case "CLAUDE_CODE_ENTRYPOINT":
			if in.OriginalEnvironment[key] != "sdk" && in.OriginalEnvironment[key] != "sdk-cli" && (in.Profile != NativeProcessProfile || in.OriginalEnvironment[key] != "cli" && !(in.OriginalEnvironment[key] == "mcp" && in.QuerySource == "workflow_agent") && !(in.OriginalEnvironment[key] == "claude-desktop" && in.NativeClaudeProof.Current())) {
				return capError("unproved original environment policy " + key)
			}
			// SDK/ordinary CLI entrypoint is billing/transport metadata, not a
			// preflight decision. It remains in the original host and model request.
		case "DISABLE_COMPACT", "DISABLE_AUTO_COMPACT", "CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE", "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "ANTHROPIC_MODEL", "USER_TYPE", "CLAUDE_INTERNAL_FC_OVERRIDES", "CLAUDE_CODE_DISABLE_1M_CONTEXT", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL":
		// These original fields belong to the caller's transport/runtime, not policy.
		case "HOME", "CLAUDE_CONFIG_DIR", "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "API_TIMEOUT_MS", "CLAUDE_CODE_MAX_RETRIES", "CLAUDE_CODE_DISABLE_FAST_MODE", "NODE_EXTRA_CA_CERTS":
		default:
			return capError("unproved original environment policy " + key)
		}
	}
	return nil
}
func modelEnvironment(env map[string]string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"CLAUDE_CODE_DISABLE_1M_CONTEXT", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL"} {
		if v, ok := env[k]; ok {
			out[k] = v
		}
	}
	return out
}
func appendContext(prompt []json.RawMessage, ctx protocol.JSONObject) ([]json.RawMessage, error) {
	output := make([]json.RawMessage, 0, len(prompt)+1)
	for _, v := range prompt {
		if truthy(v) {
			if _, err := protocol.DecodeJavaScriptJSONString(v); err != nil {
				return nil, capError("nonstring system prompt")
			}
			output = append(output, append(json.RawMessage(nil), v...))
		}
	}
	units := []uint16{}
	for i, p := range ctx.OwnProperties() {
		// OwnProperties guarantees quoted property names.
		key, _ := protocol.DecodeJavaScriptJSONString(p.NameJSON)
		value, err := protocol.ExactJavaScriptStringJSON(p.Value)
		if err != nil {
			return nil, err
		}
		// ExactJavaScriptStringJSON guarantees JSON string on success.
		v, _ := protocol.DecodeJavaScriptJSONString(value)
		if i > 0 {
			units = append(units, '\n')
		}
		units = append(units, key...)
		units = append(units, ':', ' ')
		units = append(units, v...)
	}
	if len(units) > 0 {
		output = append(output, protocol.QuoteJavaScriptUTF16(units))
	}
	return output, nil
}
func fixedEstimate(in Input) (float64, error) {
	obj := protocol.NewJSONObject()
	raw, err := json.Marshal(append([]json.RawMessage{}, in.SystemPrompt...))
	if err != nil {
		return 0, err
	}
	obj.Set("systemPrompt", raw)
	raw, err = in.SystemContext.MarshalJSON()
	if err != nil {
		return 0, err
	}
	obj.Set("systemContext", raw)
	raw, err = in.UserContext.MarshalJSON()
	if err != nil {
		return 0, err
	}
	obj.Set("userContext", raw)
	tools := []protocol.JSONObject{}
	for _, definition := range in.Tools {
		schema, e := toolschema.OriginalInputJSONSchema(definition)
		if e != nil {
			return 0, e
		}
		tools = append(tools, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quote(definition.Name)}, protocol.JSONField{Name: "input_schema", Value: schema}))
	}
	raw, err = json.Marshal(tools)
	if err != nil {
		return 0, err
	}
	obj.Set("tools", raw)
	raw, err = obj.MarshalJSON()
	if err != nil {
		return 0, err
	}
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return 0, err
	}
	return preflightcore.RoughContent(quote(string(raw)))
}
func quote(v string) json.RawMessage { raw, _ := json.Marshal(v); return raw }
func truthy(raw json.RawMessage) bool {
	if raw == nil || string(raw) == "null" || string(raw) == "false" {
		return false
	}
	if string(raw) == "true" {
		return true
	}
	if len(raw) > 0 && raw[0] == '"' {
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		return len(units) > 0
	}
	if len(raw) > 0 && (raw[0] == '{' || raw[0] == '[') {
		return true
	}
	n, _ := strconv.ParseFloat(string(raw), 64)
	return n != 0 && !math.IsNaN(n)
}
func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func projectionRecords(p preflightcore.Projection) []protocol.JSONObject {
	records := make([]protocol.JSONObject, 0, len(p.Records))
	for _, v := range p.Records {
		records = append(records, v.Record)
	}
	return records
}
