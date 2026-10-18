// Package requestretry connects original direct-relay inputs to physical request
// creation. It never retries consumption of a successful stream or accepts a
// prepared Source request, resolved Source credential, or fabricated API fault.
package requestretry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

type NoticeAction uint8

const (
	Resume NoticeAction = iota + 1
	Stop
)

var ErrConsumerStopped = errors.New("request retry consumer stopped")

type CapabilityError struct{ Boundary string }

func (failure *CapabilityError) Error() string {
	return "request retry capability is not migrated: " + failure.Boundary
}
func unsupported(boundary string) error { return &CapabilityError{Boundary: boundary} }

type Input struct {
	FallbackModel string `json:"FallbackModel,omitempty"`
	Context       requestcontext.Context
	Runtime       provider.SourceRuntime
	Auth          OriginalAuthInputs
	// ResolvedFastMode is sampled by the actual original host AppState on owner.
	// nil requires the legacy explicit disable flag; true remains unsupported.
	ResolvedFastMode *bool `json:"ResolvedFastMode,omitempty"`
}

// OriginalAuthInputs is the unprocessed CLI flag state. A real host must pass
// supplied flags, not a Source-selected helper/credential or a resolved absence.
// Active flag settings require the real settings/helper lifecycle, not a no-op.
type OriginalAuthInputs struct {
	FlagSettingsPath   string
	FlagSettingsInline json.RawMessage
}

// NativeOpenIOFactory separates original owner admission from client creation.
// CheckOwner must validate the currently active canonical scope without effects;
// NewNativeOpenIO constructs its bridge only in the real GetClient path.
type NativeOpenIOFactory interface {
	CheckOwner() error
	NewNativeOpenIO() (*provider.OwnerNativeIO, error)
}

type Dependencies struct {
	NativeParentClientState *bootstrapstate.NativeParentClientState
	// ObserveOpenFailure receives the final failed Open result before UI
	// presentation. It cannot change that error or the attempted request.
	ObserveOpenFailure        func(error)
	NativeThinkingSummaryBeta func(*cooperative.Scope, string) (*bool, error)
	NativeRetryEnvironment    map[string]string
	NativeFastModeProof       *cooperative.NativeFastModeRequest
	NativeFastState           *retrypolicy.FastState
	NativeFastEffect          func(retrypolicy.FastEffect) error
	NativeFastError           error
	NativeBedrock             *NativeBedrock
	NativeVertex              *NativeVertex
	NativeCloudError          error
	NativeAuthHelper          *NativeAuthHelper
	NativeClaudeAuth          *NativeClaudeAuth
	NativeClaudeError         error
	NativeAccountUUID         func() (string, error)
	NativeCompactSource       *cooperative.NativeCompactModels
	APIAttemptBudget          *APIAttemptBudget
	// The host installs the original credential consumer for this current ASK.
	// Main turns may already carry their shared turn-state binding in Host.
	NativeCodexOAuth *provider.OpenAICodexConfig
	NativeGrokOAuth  *provider.GrokOAuthConfig
	// NativeWebSource is supplied only by the actual owner-bound Web tool model
	// consumer; an arbitrary auxiliary querySource cannot unlock admission.
	NativeWebSource              string
	NativePromptHookSource       *cooperative.NativePromptHookModels
	NativeAgentHookSource        *cooperative.NativeAgentHookModels
	NativeInsightsSource         *cooperative.NativeInsightsModels
	NativeRemoteTitleSource      *cooperative.NativeRemoteTitleModels
	NativeSideQuestionSource     *cooperative.NativeSideQuestionModels
	NativeAutoDreamSource        *cooperative.NativeAutoDreamModels
	NativePromptSuggestionSource *cooperative.NativeSideQuestionModels
	NativeAgentSummarySource     *cooperative.NativeSideQuestionModels
	// StreamOwner/StreamScope bind the current admitted model stack. They carry
	// no prepared inputs and must be refreshed for every original ASK.
	StreamOwner *cooperative.Owner
	StreamScope *cooperative.Scope
	UUID        func() string
	Now         func() time.Time
	Random      func() float64
	Sleep       func(context.Context, float64) error
	Notice      func(context.Context, retrypolicy.RetryNotice) (NoticeAction, error)
	// One process-owned memo is shared by CLI/Server client lifetimes. The
	// original environment and file reader, not a prepared trust pool, feed it.
	TLSState    *tlsconfig.State
	OpenIOAwait provider.OwnerAwait
	// BodyIOAwait is independent native-response bytes; it never opens headers.
	BodyIOAwait provider.OwnerAwait
	// NativeOpenIO is an original host bridge factory invoked inside GetClient.
	// It must bind that current Owner/Scope; retaining a prior ASK scope fails.
	// Mutually exclusive with the legacy coarse OpenIOAwait adapter.
	NativeOpenIO NativeOpenIOFactory
	// Observers cannot supply prepared requests, credentials or replacement headers.
	OnClient      func()
	OnKeyLookup   func()
	OnAttempt     func(float64)
	OnPreparation func([]protocol.JSONObject)
	// OnPrepared runs once after Prepare, before any physical client/key lookup.
	// It is a production pre-request boundary and returns the original error.
	OnPrepared func([]protocol.JSONObject) error
	// OnParamsPrepared observes the actual first native request after construction,
	// before GetClient. That Result is reused by the first physical attempt.
	// It is not an alternate constructor or a callback for subsequent retries.
	OnParamsPrepared func(*requestcontext.Result) error
	// PromptSnapshot only observes the actual prepared request; it must not await disk.
	PromptSnapshot   func(json.RawMessage)
	ObserveRequest   func(string, http.Header, json.RawMessage)
	ObserveFetch     func(tracecapture.FetchInput)
	ObserveFailure   func(error)
	Trace            *tracecapture.Service
	TraceEnvironment map[string]string
}

// NativeKeepAliveDisable returns the concrete host factory's captured-process
// policy effect. Legacy finite fresh-connection fixtures have no shared pool.
func (deps Dependencies) NativeKeepAliveDisable() func() error {
	if native, ok := deps.NativeOpenIO.(interface{ NativeKeepAliveDisable() func() error }); ok {
		return native.NativeKeepAliveDisable()
	}
	return nil
}

// The physical direct-env-key slice includes headless and original bare
// interactive sessions, migrated native definitions, original extra schemas and
// textual tool history. Credential sources outside the finite profile stop.
// The distinct component views are derived from these original, validated names.
var environment = map[string]bool{
	"ANTHROPIC_BASE_URL": true, "ANTHROPIC_API_KEY": true, "ANTHROPIC_CUSTOM_HEADERS": true, "API_TIMEOUT_MS": true,
	"CLAUDE_CODE_EXTRA_METADATA": true, "CLAUDE_CODE_MAX_RETRIES": true, "CLAUDE_CODE_DISABLE_FAST_MODE": true,
	"CLAUDE_CODE_SIMPLE": true,
}

var tlsEnvironment = map[string]bool{
	"NODE_EXTRA_CA_CERTS": true, "NODE_OPTIONS": true,
	"CLAUDE_CODE_CLIENT_CERT": true, "CLAUDE_CODE_CLIENT_KEY": true, "CLAUDE_CODE_CLIENT_KEY_PASSPHRASE": true,
	"NODE_TLS_REJECT_UNAUTHORIZED": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	"OPENSSL_CONF": true, "OPENSSL_MODULES": true,
}

func isHTTPS(input Input) bool {
	if input.Context.Host.NativeVertexBound {
		base := input.Context.Host.Env["ANTHROPIC_VERTEX_BASE_URL"]
		return base == "" || strings.HasPrefix(strings.ToLower(base), "https:")
	}
	if input.Context.Host.NativeBedrockBound {
		base := input.Context.Host.Env["ANTHROPIC_BEDROCK_BASE_URL"]
		return base == "" || strings.HasPrefix(strings.ToLower(base), "https:")
	}
	if (input.Context.Host.NativeCodexOAuth != nil || input.Context.Host.NativeGrokOAuth != nil || input.Context.Host.NativeClaudeSubscriber && input.Context.Host.NativeClaudeProof.Current()) && input.Context.Host.Env["ANTHROPIC_BASE_URL"] == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(input.Context.Host.Env["ANTHROPIC_BASE_URL"]), "https:")
}

func validated(input Input, deps Dependencies) error {
	if deps.NativeCloudError != nil {
		return deps.NativeCloudError
	}
	native := input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile
	if deps.NativeParentClientState != nil {
		if !native || !deps.NativeParentClientState.Owns(deps.StreamOwner) {
			return cooperative.ErrRestricted
		}
		if _, err := deps.NativeParentClientState.SessionID(deps.StreamScope); err != nil {
			return err
		}
	}
	compact := native && input.Context.Options.QuerySource == "compact" && deps.NativeCompactSource.Owns(deps.StreamOwner)
	if deps.APIAttemptBudget != nil && (!compact || deps.StreamScope == nil || !deps.StreamScope.BelongsTo(deps.StreamOwner)) {
		return cooperative.ErrRestricted
	}
	if deps.NativeVertex != nil && (!native || !deps.NativeVertex.Owns(deps.StreamOwner, deps.StreamScope)) {
		return cooperative.ErrRestricted
	}
	if input.Context.Host.NativeVertexBound && (deps.NativeVertex == nil || !deps.NativeVertex.Owns(deps.StreamOwner, deps.StreamScope)) {
		return unsupported("Vertex requires actual current native bridge")
	}
	if deps.NativeBedrock != nil && (!native || !deps.NativeBedrock.Owns(deps.StreamOwner, deps.StreamScope)) {
		return cooperative.ErrRestricted
	}
	if input.Context.Host.NativeBedrockBound && (deps.NativeBedrock == nil || !deps.NativeBedrock.Owns(deps.StreamOwner, deps.StreamScope)) {
		return unsupported("Bedrock requires actual current native bridge")
	}
	if deps.NativeClaudeAuth != nil && (!native || !deps.NativeClaudeAuth.Owns(deps.StreamOwner, deps.StreamScope)) {
		return cooperative.ErrRestricted
	}
	if deps.NativeAuthHelper != nil {
		if !native {
			return unsupported("native API key helper on finite profile")
		}
		if err := deps.NativeAuthHelper.check(deps.StreamOwner, deps.StreamScope); err != nil {
			return err
		}
	}
	if deps.OnParamsPrepared != nil && !native {
		return unsupported("native prepared params callback on finite profile")
	}
	if native && input.Context.Host.NativeCodexOAuth == nil && input.Context.Host.NativeGrokOAuth == nil {
		target := input.Context.Host.Env["ANTHROPIC_BASE_URL"]
		if input.Context.Host.NativeBedrockBound {
			target = input.Context.Host.Env["ANTHROPIC_BEDROCK_BASE_URL"]
		}
		if input.Context.Host.NativeVertexBound {
			target = input.Context.Host.Env["ANTHROPIC_VERTEX_BASE_URL"]
		}
		if _, e := provider.NativeDirectHTTPExchangeFromEnvironment(target, input.Context.Host.Env); e != nil {
			return e
		}
	}
	if deps.UUID == nil || deps.Now == nil || deps.Random == nil || deps.Sleep == nil || deps.Notice == nil {
		return unsupported("clock/ID/RNG/sleep/notice host")
	}
	if input.Context.Thinking.Type != "disabled" && (!native || input.Context.Thinking.Type != "enabled" && input.Context.Thinking.Type != "adaptive") {
		return unsupported("disabled-thinking profile")
	}
	if simple, present := input.Context.Host.Env["CLAUDE_CODE_SIMPLE"]; present && simple != "1" && !native {
		return unsupported("original bare environment value")
	}
	remoteTitle := native && deps.NativeRemoteTitleSource.AcceptsPurpose(input.Context.Options.QuerySource) && deps.NativeRemoteTitleSource.Owns(deps.StreamOwner) && deps.StreamScope.BelongsTo(deps.StreamOwner)
	if !input.Context.Options.IsNonInteractiveSession && input.Context.Host.Env["CLAUDE_CODE_SIMPLE"] != "1" && !remoteTitle {
		return unsupported("non-bare interactive approval/credential lifecycle")
	}
	if input.Auth.FlagSettingsPath != "" || len(bytes.TrimSpace(input.Auth.FlagSettingsInline)) > 0 && !bytes.Equal(bytes.TrimSpace(input.Auth.FlagSettingsInline), []byte("null")) {
		return unsupported("original flag settings/helper lifecycle")
	}
	for _, schema := range input.Context.Options.ExtraToolSchemas {
		var object protocol.JSONObject
		if json.Unmarshal(schema, &object) != nil || !object.IsObject() {
			return unsupported("original extra tool schema object")
		}
		if name, err := protocol.DecodeJavaScriptJSONString(object.Get("name")); err != nil || len(name) == 0 {
			return unsupported("original extra tool schema name")
		}
	}
	if raw := input.Context.Options.ToolChoice; len(raw) > 0 {
		var choice protocol.JSONObject
		if json.Unmarshal(raw, &choice) != nil || !choice.IsObject() {
			return unsupported("original tool choice object")
		}
		switch choice.String("type") {
		case "auto", "any":
		case "tool":
			if name, err := protocol.DecodeJavaScriptJSONString(choice.Get("name")); err != nil || len(name) == 0 {
				return unsupported("original tool choice name")
			}
		default:
			return unsupported("original tool choice type")
		}
	}
	if input.ResolvedFastMode != nil && *input.ResolvedFastMode && !input.Context.Host.NativeFastModeProof.Owns(deps.StreamOwner) {
		return unsupported("active resolved fast mode")
	}
	if input.Context.Host.Env["CLAUDE_CODE_DISABLE_FAST_MODE"] != "1" && (input.Context.Host.Env["CLAUDE_CODE_DISABLE_FAST_MODE"] != "" || input.ResolvedFastMode == nil) {
		return unsupported("fast/persistent retry host")
	}
	for name := range input.Context.Host.Env {
		if name == "CLAUDE_CODE_ENTRYPOINT" {
			querySource := input.Context.Options.QuerySource
			child := native && (querySource == "agent:custom" || strings.HasPrefix(querySource, "agent:builtin:") && len(querySource) > len("agent:builtin:") || querySource == "workflow_agent")
			web := native && deps.NativeWebSource == querySource && (querySource == "web_fetch_apply" || querySource == "web_search_tool")
			hook := native && querySource == "hook_prompt" && deps.NativePromptHookSource.Owns(deps.StreamOwner)
			agentHook := native && querySource == "hook_agent" && deps.NativeAgentHookSource.Owns(deps.StreamOwner)
			insights := native && querySource == "insights" && deps.NativeInsightsSource.Owns(deps.StreamOwner)
			remoteTitle := native && deps.NativeRemoteTitleSource.AcceptsPurpose(querySource) && deps.NativeRemoteTitleSource.Owns(deps.StreamOwner)
			sideQuestion := native && querySource == "side_question" && deps.NativeSideQuestionSource.Owns(deps.StreamOwner)
			autoDream := native && querySource == "auto_dream" && deps.NativeAutoDreamSource.Current(deps.StreamScope)
			promptSuggestion := native && querySource == "prompt_suggestion" && deps.NativePromptSuggestionSource.Current(deps.StreamScope)
			agentSummary := native && querySource == "agent_summary" && deps.NativeAgentSummarySource.Current(deps.StreamScope)
			entry := input.Context.Host.Env[name]
			if ((entry != "sdk" && entry != "sdk-cli" && (!native || entry != "cli" && !(entry == "mcp" && (web || child && querySource == "workflow_agent")) && !(entry == "claude-desktop" && deps.NativeClaudeAuth.Owns(deps.StreamOwner, deps.StreamScope)))) || input.Context.Options.QuerySource != "sdk" && !child && !web && !hook && !agentHook && !insights && !remoteTitle && !sideQuestion && !autoDream && !promptSuggestion && !agentSummary) && !compact {
				return unsupported("original runtime entrypoint value")
			}
			continue
		}
		if !environment[name] && !tlsEnvironment[name] && !(native && (requestcontext.IsNativePolicyEnvironment(name) || input.Context.Host.NativeBedrockBound && requestcontext.IsNativeBedrockEnvironment(name) || input.Context.Host.NativeVertexBound && requestcontext.IsNativeVertexEnvironment(name) || provider.IsNativeProxyEnvironment(name) || name == "CLAUDE_CODE_SIMPLE" || input.Context.Host.NativeCodexOAuth != nil && IsOpenAIClientEnvironment(name) || input.Context.Host.NativeGrokOAuth != nil && IsGrokClientEnvironment(name))) {
			return unsupported("original environment " + name)
		}
		boundClaudeTLS := native && input.Context.Host.NativeClaudeSubscriber && deps.NativeClaudeAuth.Owns(deps.StreamOwner, deps.StreamScope) && deps.TLSState != nil
		if tlsEnvironment[name] && !isHTTPS(input) && input.Context.Host.NativeCodexOAuth == nil && input.Context.Host.NativeGrokOAuth == nil && !boundClaudeTLS && input.Context.Host.Env[name] != "" {
			return unsupported("original TLS options on an HTTP target")
		}
	}
	if isHTTPS(input) {
		if deps.TLSState == nil {
			return unsupported("process-owned TLS configuration memo")
		}
		if err := tlsconfig.ValidateOriginalTLSOptions(input.Context.Host.Env); err != nil {
			return unsupported(err.Error())
		}
	}
	for _, envelope := range input.Context.History {
		kind := envelope.String("type")
		if kind == "attachment" {
			if attachments.Invisible(envelope.Get("attachment")) {
				continue
			}
			var payload protocol.JSONObject
			if err := json.Unmarshal(envelope.Get("attachment"), &payload); err != nil || !payload.IsObject() {
				return unsupported("retained attachment payload")
			}
			switch payload.String("type") {
			case "diagnostics":
				if !native {
					return unsupported("native LSP diagnostic attachment renderer")
				}
				continue
			case "teammate_mailbox":
				if !native {
					return unsupported("native teammate-mailbox attachment renderer")
				}
				continue
			case "queued_command":
				if !native {
					return unsupported("native queued-command attachment renderer")
				}
				continue
			case "plan_mode", "plan_mode_reentry", "plan_mode_exit":
				if input.Context.Host.PlanOptions == nil {
					return unsupported("original plan attachment renderer bindings")
				}
				continue
			case "auto_mode", "auto_mode_exit", "invoked_skills", "skill_listing", "deferred_tools_delta", "workflow_keyword_request", "ultra_effort_enter", "ultra_effort_exit", "companion_intro":
				// Prepare owns actual renderer coercion and typed malformed-drop
				// behavior. Never accept Source-rendered replacement history here.
				continue
			case "file", "directory", "agent_mention":
				if input.Context.Host.PlanOptions == nil || input.Context.Host.PlanOptions.MapToolResult == nil {
					return unsupported("original file/mention attachment renderer bindings")
				}
				continue
			case "mcp_resource":
				continue
			case "hook_stopped_continuation", "hook_blocking_error", "hook_additional_context", "hook_success", "async_hook_response":
				continue
			default:
				return unsupported("retained attachment kind " + payload.String("type"))
			}
		}
		if kind == "progress" && native {
			var data protocol.JSONObject
			if data.UnmarshalJSON(envelope.Get("data")) == nil && data.IsObject() && (data.String("type") == "query_update" || data.String("type") == "search_results_received" || nativeTaskOutputHistoryProgress(envelope, data) || messages.IsNativeBashProgress(envelope) || messages.IsNativeHookRecord(envelope)) {
				continue
			}
		}
		if native && messages.IsNativeHookRecord(envelope) {
			continue
		}
		if native && messages.IsNativeAPIRetryRecord(envelope) {
			continue
		}
		if native && messages.IsNativeStreamRecoveryRecord(envelope) {
			continue
		}
		if native && messages.IsNativeUserPromptBlockedRecord(envelope) {
			continue
		}
		if native && messages.IsNativeModelFallbackRecord(envelope) {
			continue
		}
		if native && messages.IsNativeMemorySavedRecord(envelope) {
			continue
		}
		// Original compact markers remain in the host history until Prepare.
		// Its already migrated normalizer filters this exact system class; do
		// not strip the marker here or admit unrelated system capabilities.
		if kind == "system" && envelope.String("subtype") == "compact_boundary" && envelope.Get("isApiErrorMessage") == nil && envelope.Get("isVirtual") == nil {
			continue
		}
		if kind == "system" && envelope.String("subtype") == "local_command" && envelope.Get("isApiErrorMessage") == nil && envelope.Get("isVirtual") == nil {
			if _, e := protocol.DecodeJavaScriptJSONString(envelope.Get("content")); e == nil && envelope.String("uuid") != "" && envelope.String("timestamp") != "" {
				continue
			}
		}
		synthetic := requestcontext.IsSyntheticAPIErrorMessage(envelope)
		if kind != "user" && kind != "assistant" || envelope.Get("isApiErrorMessage") != nil && !synthetic && !requestcontext.IsOriginalNoResponseAssistant(envelope) || envelope.Get("isVirtual") != nil {
			return unsupported("unported history envelope classification")
		}
		if synthetic {
			// Original host history remains intact. Prepare owns exact filtering and
			// its unported anchor-lookup error guards; it never sends this content.
			continue
		}
		var message protocol.JSONObject
		if err := json.Unmarshal(envelope.Get("message"), &message); err != nil || !message.IsObject() {
			return unsupported("history message")
		}
		raw := message.Get("content")
		if _, err := protocol.DecodeJavaScriptJSONString(raw); err == nil {
			continue
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			return unsupported("ordinary text content")
		}
		var blocks []protocol.JSONObject
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return unsupported("ordinary text content")
		}
		for _, block := range blocks {
			if block.String("type") == "image" && kind == "user" {
				if err := requestcontext.ValidateProcessedImageBlock(block); err != nil {
					return unsupported("unported history media/tool result")
				}
				continue
			}
			if block.String("type") == "document" && kind == "user" {
				if err := requestcontext.ValidateDocumentBlock(block); err != nil {
					return unsupported("unported history media/tool result")
				}
				continue
			}
			if block.String("type") == "tool_use" && kind == "assistant" {
				for _, name := range []string{"id", "name"} {
					if _, err := protocol.DecodeJavaScriptJSONString(block.Get(name)); err != nil {
						return unsupported("historical tool id/name")
					}
				}
				continue
			}
			if block.String("type") == "tool_result" && kind == "user" {
				if _, err := protocol.DecodeJavaScriptJSONString(block.Get("tool_use_id")); err != nil {
					return unsupported("historical tool result id")
				}
				value := block.Get("content")
				if len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					continue
				}
				if _, err := protocol.DecodeJavaScriptJSONString(value); err == nil {
					continue
				}
				var nested []protocol.JSONObject
				if json.Unmarshal(value, &nested) != nil || nested == nil {
					return unsupported("textual tool result content")
				}
				for _, part := range nested {
					if part.String("type") == "image" {
						if err := requestcontext.ValidateProcessedImageBlock(part); err != nil {
							return unsupported("unported nested tool result media")
						}
						continue
					}
					if part.String("type") == "document" {
						if err := requestcontext.ValidateDocumentBlock(part); err != nil {
							return unsupported("unported nested tool result media")
						}
						continue
					}
					if part.String("type") == "tool_reference" {
						continue
					}
					if part.String("type") != "text" {
						return unsupported("unported nested tool result media")
					}
					if _, err := protocol.DecodeJavaScriptJSONString(part.Get("text")); err != nil {
						return unsupported("nested tool result text string")
					}
				}
				continue
			}
			// Prepare already retains original assistant protected blocks and
			// their raw properties. Current generation still requires disabled
			// thinking; this admits history, not a new model/host policy.
			if kind == "assistant" && (block.String("type") == "thinking" || block.String("type") == "redacted_thinking") {
				continue
			}
			if block.String("type") != "text" {
				return unsupported("unported history media/tool result")
			}
			if _, err := protocol.DecodeJavaScriptJSONString(block.Get("text")); err != nil {
				return unsupported("text string")
			}
		}
	}
	return nil
}

// resolveOriginalEnvKey independently implements auth.ts's headless non-IDE and
// original bare ANTHROPIC_API_KEY branches after original admission. Interactive
// non-bare approval, helper/FD/OAuth/keychain sources stop explicitly.
func resolveOriginalEnvKey(input Input) (*string, error) {
	key := input.Context.Host.Env["ANTHROPIC_API_KEY"]
	if key == "" {
		if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile && input.Context.Host.Env["ANTHROPIC_AUTH_TOKEN"] != "" {
			// The original Anthropic constructor reads this direct env token.
			// An absent fallback key must remain nil rather than an invented key.
			return nil, nil
		}
		return nil, unsupported("missing original environment API key; helper/FD/OAuth/keychain/approval")
	}
	return &key, nil
}

func Open(ctx context.Context, input Input, deps Dependencies) (stream *provider.RawMessageStream, failure error) {
	defer func() {
		if failure != nil && deps.ObserveOpenFailure != nil {
			deps.ObserveOpenFailure(failure)
		}
	}()
	return openMessages(ctx, input, deps, nil)
}

type nativeFallbackOperation struct {
	response   provider.MessageResponse
	initial529 float64
}

func CreateNativeFallback(ctx context.Context, input Input, deps Dependencies, initial529 float64) (provider.MessageResponse, error) {
	if input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile || deps.StreamScope == nil || !deps.StreamScope.BelongsTo(deps.StreamOwner) {
		return provider.MessageResponse{}, cooperative.ErrRestricted
	}
	fallback := &nativeFallbackOperation{initial529: initial529}
	_, err := openMessages(ctx, input, deps, fallback)
	return fallback.response, err
}

func openMessages(ctx context.Context, input Input, deps Dependencies, fallback *nativeFallbackOperation) (*provider.RawMessageStream, error) {
	if deps.NativeFastError != nil {
		return nil, deps.NativeFastError
	}
	if input.Context.Host.NativeFastModeProof == nil {
		input.Context.Host.NativeFastModeProof = deps.NativeFastModeProof
	}
	if ctx == nil {
		return nil, unsupported("caller context")
	}
	if err := BindNativeClaudeHost(ctx, &input, deps); err != nil {
		return nil, err
	}
	if err := BindNativeVertexHost(&input, deps); err != nil {
		return nil, err
	}
	if err := BindNativeBedrockHost(&input, deps); err != nil {
		return nil, err
	}
	if input.Context.Host.NativeCodexOAuth == nil {
		input.Context.Host.NativeCodexOAuth = deps.NativeCodexOAuth
	}
	if input.Context.Host.NativeGrokOAuth == nil {
		input.Context.Host.NativeGrokOAuth = deps.NativeGrokOAuth
	}
	if deps.BodyIOAwait != nil && (deps.NativeOpenIO == nil || deps.OpenIOAwait != nil) {
		return nil, unsupported("native body owner requires one original native headers bridge")
	}
	if deps.NativeOpenIO != nil && deps.OpenIOAwait != nil {
		return nil, unsupported("ambiguous original native/legacy owner bridge")
	}
	if deps.NativeOpenIO != nil {
		if err := deps.NativeOpenIO.CheckOwner(); err != nil {
			return nil, err
		}
	}
	if err := validated(input, deps); err != nil {
		return nil, err
	}
	request := input.Context
	if deps.NativeAccountUUID != nil {
		if input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile || deps.StreamScope == nil || !deps.StreamScope.BelongsTo(deps.StreamOwner) {
			return nil, cooperative.ErrRestricted
		}
		account, err := deps.NativeAccountUUID()
		if err != nil {
			return nil, err
		}
		request.Host.OAuthAccountUUID = &account
	}
	request.Host.Env = map[string]string{}
	clientEnv := map[string]string{}
	policyEnv := map[string]string{}
	for name, value := range input.Context.Host.Env {
		// Bare mode remains original host/auth state. It is not client/header or
		// request-body environment. Never discard unknown names to admit a profile.
		if name == "CLAUDE_CODE_SIMPLE" {
			continue
		} else if name == "CLAUDE_CODE_MAX_RETRIES" || name == "CLAUDE_CODE_DISABLE_FAST_MODE" {
			policyEnv[name] = value
		} else {
			clientEnv[name] = value
			if !tlsEnvironment[name] && !provider.IsNativeProxyEnvironment(name) && !IsOpenAIClientEnvironment(name) {
				request.Host.Env[name] = value
			}
		}
	}
	if deps.NativeRetryEnvironment != nil {
		if input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile || deps.StreamScope == nil || !deps.StreamScope.BelongsTo(deps.StreamOwner) {
			return nil, cooperative.ErrRestricted
		}
		for key, value := range deps.NativeRetryEnvironment {
			switch key {
			case "FALLBACK_FOR_ALL_PRIMARY_MODELS", "USER_TYPE", "IS_SANDBOX", "CLAUDE_CODE_REMOTE":
				policyEnv[key] = value
			default:
				return nil, unsupported("unrecognized native retry policy environment " + key)
			}
		}
	}
	preparation, err := requestcontext.Prepare(request, requestcontext.Dependencies{UUID: deps.UUID, Now: deps.Now})
	if err != nil {
		return nil, err
	}
	if deps.OnPreparation != nil {
		deps.OnPreparation(preparation.NormalizedHistory())
	}
	if deps.OnPrepared != nil {
		if err := deps.OnPrepared(preparation.NormalizedHistory()); err != nil {
			return nil, err
		}
	}
	var firstResult *requestcontext.Result
	if request.Host.Profile == requestcontext.NativeDirectRelayProfile {
		// Source captures query log scalars from paramsFromContext before client
		// creation. Keep one actual first Result, rather than rebuilding it for
		// logging or normalizing history twice. Later retries still rebuild their
		// retry-sensitive fields from the same PreparedContext.
		firstResult, err = preparation.BuildAttempt(requestcontext.AttemptOptions{Model: request.Options.Model, FastMode: input.ResolvedFastMode})
		if err != nil {
			return nil, err
		}
		if deps.PromptSnapshot != nil && firstResult.PreparedRequest != nil {
			deps.PromptSnapshot(firstResult.PreparedRequest.Body)
		}
		if deps.OnParamsPrepared != nil {
			if err = deps.OnParamsPrepared(firstResult); err != nil {
				return nil, err
			}
		}
	}
	thinking, _ := json.Marshal(map[string]string{"type": input.Context.Thinking.Type})
	querySource := request.Options.QuerySource
	options := retrypolicy.AttemptOptions{Model: request.Options.Model, ThinkingConfig: thinking, QuerySource: &querySource, FastMode: input.ResolvedFastMode, FallbackModel: input.FallbackModel}
	if fallback != nil {
		options.InitialConsecutive529Errors = fallback.initial529
	}
	host := retrypolicy.AttemptHost{Profile: retrypolicy.AttemptDirectRelayProfile, Env: policyEnv, Fast: &retrypolicy.FastState{}}
	if input.Context.Host.NativeFastModeProof.Owns(deps.StreamOwner) {
		if deps.NativeFastState == nil || deps.NativeFastEffect == nil {
			return nil, unsupported("actual fast runtime retry state/effects")
		}
		host.Fast = deps.NativeFastState
	}
	var prepared *provider.MessagesRequest
	callbacks := retrypolicy.AttemptDependencies[*provider.Anthropic]{
		UUID: deps.UUID, Now: deps.Now, Random: deps.Random, Sleep: deps.Sleep, FastEffect: deps.NativeFastEffect,
		ConsumeAttempt: deps.APIAttemptBudget.Consume,
		Yield: func(notice retrypolicy.RetryNotice) error {
			action, err := deps.Notice(ctx, notice)
			if err != nil {
				return err
			}
			switch action {
			case Resume:
				return nil
			case Stop:
				return ErrConsumerStopped
			default:
				return unsupported("notice consumer action")
			}
		},
		GetClient: func(context.Context) (*provider.Anthropic, error) {
			return originalClient(ctx, input, deps, clientEnv, request, func() *provider.MessagesRequest { return prepared })
		},
	}
	if deps.NativeAuthHelper != nil {
		callbacks.InvalidateAPIKeyHelper = func() error {
			if err := deps.NativeAuthHelper.check(deps.StreamOwner, deps.StreamScope); err != nil {
				return err
			}
			return deps.NativeAuthHelper.clear()
		}
	}
	callbacks.DisableKeepAlive = deps.NativeKeepAliveDisable()
	return retrypolicy.RunAttempts(ctx, options, host, callbacks, func(client *provider.Anthropic, attempt float64, retry *retrypolicy.RetryContext) (*provider.RawMessageStream, error) {
		if deps.OnAttempt != nil {
			deps.OnAttempt(attempt)
		}
		result := firstResult
		firstResult = nil
		if deps.NativeParentClientState != nil {
			if err := preparation.RefreshNativeParentMetadataOnOwner(deps.StreamScope, deps.NativeParentClientState); err != nil {
				return nil, err
			}
			// Source samples metadata again after GetClient for the physical
			// attempt. History/schema preparation remains once per query.
			result = nil
		}
		if result == nil {
			var err error
			result, err = preparation.BuildAttempt(requestcontext.AttemptOptions{Model: retry.Model, MaxTokensOverride: retry.MaxTokensOverride, FastMode: retry.FastMode})
			if err != nil {
				return nil, err
			}
		}
		prepared = result.PreparedRequest
		if fallback != nil {
			var err error
			prepared, err = prepareNativeFallback(result, request.Host.NativeCodexOAuth != nil || request.Host.NativeGrokOAuth != nil)
			if err != nil {
				return nil, err
			}
			timeout := retrypolicy.DefaultMaximum(input.Context.Host.Env["API_TIMEOUT_MS"])
			if input.Context.Host.Env["API_TIMEOUT_MS"] == "" || math.IsNaN(timeout) || timeout == 0 {
				timeout = 300000
			}
			await := deps.BodyIOAwait
			if await == nil {
				await = func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
					return deps.StreamScope.AwaitBytes(cooperative.StreamNext, work)
				}
			}
			fallback.response, err = client.CreateNativePreparedJSON(ctx, prepared, timeout, await)
			if err != nil && deps.ObserveFailure != nil {
				deps.ObserveFailure(err)
			}
			return nil, err
		}
		stream, err := client.OpenMessagesStream(ctx, prepared)
		if err != nil && deps.ObserveFailure != nil {
			deps.ObserveFailure(err)
		}
		return stream, err
	})
}

func IsCallerAbort(failure error) bool {
	var sdk *provider.SDKStreamError
	return errors.As(failure, &sdk) && sdk != nil && sdk.Class == "APIUserAbortError"
}

var _ error = (*CapabilityError)(nil)

func nativeTaskOutputHistoryProgress(record, data protocol.JSONObject) bool {
	if data.String("type") != "waiting_for_task" || len(data.OwnProperties()) != 3 {
		return false
	}
	for _, key := range []string{"taskDescription", "taskType"} {
		if _, e := protocol.DecodeJavaScriptJSONString(data.Get(key)); e != nil {
			return false
		}
	}
	return strings.HasPrefix(record.String("toolUseID"), "task-output-waiting-")
}
