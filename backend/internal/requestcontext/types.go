// Package requestcontext constructs requests from original session inputs.
// It never accepts a prepared Source request as a construction shortcut.
package requestcontext

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestmapping"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
)

type Thinking struct {
	Type         string   `json:"type"`
	BudgetTokens *float64 `json:"budgetTokens,omitempty"`
}

type Options struct {
	Model                   string            `json:"model"`
	QuerySource             string            `json:"querySource"`
	QueryTracking           json.RawMessage   `json:"queryTracking,omitempty"`
	IsNonInteractiveSession bool              `json:"isNonInteractiveSession"`
	HasAppendSystemPrompt   bool              `json:"hasAppendSystemPrompt"`
	EnablePromptCaching     *bool             `json:"enablePromptCaching,omitempty"`
	SkipCacheWrite          bool              `json:"skipCacheWrite"`
	MaxOutputTokensOverride *float64          `json:"maxOutputTokensOverride,omitempty"`
	TemperatureOverride     *float64          `json:"temperatureOverride,omitempty"`
	OutputFormat            json.RawMessage   `json:"outputFormat,omitempty"`
	ExtraToolSchemas        []json.RawMessage `json:"extraToolSchemas,omitempty"`
	ToolChoice              json.RawMessage   `json:"toolChoice,omitempty"`
	EffortValue             json.RawMessage   `json:"effortValue,omitempty"`
	EffortValueOverridesEnv bool              `json:"effortValueOverridesEnv,omitempty"`
}

// Host is a frozen direct-relay session snapshot, not an ambient process read.
// Its credential/provider transport construction remains with provider.
type Host struct {
	NativeFastModeProof    *cooperative.NativeFastModeRequest `json:"-"`
	NativeBedrockBound     bool                               `json:"-"` // Minted only after the current native cloud bridge is checked.
	NativeBedrockCanonical func(string) (string, error)       `json:"-"`
	NativeBedrockProof     *cooperative.NativeBedrockRequest  `json:"-"`
	NativeVertexBound      bool                               `json:"-"`
	NativeVertexCanonical  func(string) (string, error)       `json:"-"`
	NativeVertexProof      *cooperative.NativeVertexRequest   `json:"-"`
	NativeClaudeSubscriber bool                               `json:"-"`
	NativeClaudeProof      *cooperative.NativeClaudeRequest   `json:"-"`
	NativeCodexOAuth       *provider.OpenAICodexConfig        `json:"-"`
	NativeGrokOAuth        *provider.GrokOAuthConfig          `json:"-"`
	NativeToolSearch       *toolsearch.Service
	ToolSearchCatalogue    *toolsearch.Catalogue
	PlanOptions            *attachments.PlanOptions // Original runtime planning inputs; never a rendered prompt.
	Profile                string                   // Explicit caller declaration; not inferred from ambient state.
	Env                    map[string]string
	DeviceID               string
	SessionID              string
	Workload               string
	SDKBetas               []string
	TTLEligible            bool
	TTLAllowlist           []string
	ToolSchemas            *toolschema.Session      // Retained across rounds in the same session.
	ToolSchemaHost         *toolschema.Host         // Original prompt/feature inputs, never rendered schemas.
	NativeToolDefinitions  *[]toolschema.Definition // Actual executable producer declarations, outside the JSON input format.
}

const DirectRelayProfile = "direct-relay-release-default-no-experiments"
const NativeDirectRelayProfile = "native-direct-relay-process-controls-v1"

type Context struct {
	History      []protocol.JSONObject
	SystemPrompt []json.RawMessage // Original quoted JavaScript strings, UTF-16 intact.
	Tools        []protocol.JSONObject
	Thinking     Thinking
	Options      Options
	Host         Host
}

type Dependencies struct {
	UUID func() string
	Now  func() time.Time
}

// AttemptOptions is the retry scheduler's original RetryContext. Model does
// not replace Options.Model: Source captures the request model, system and
// tools before entering its creation retry loop. The declared relay profile
// has no model-specific cache gates or dynamic model experiments.
type AttemptOptions struct {
	FastMode          *bool
	Model             string
	MaxTokensOverride *float64
}

// PreparedContext owns the once-per-query normalization and schema work.
// Callers supply original Context inputs to Prepare, never prepared history
// or a captured TypeScript request. Each BuildAttempt constructs a new request.
type PreparedContext struct {
	toolSearchPlan toolsearch.RequestPlan
	input          Context
	normalized     []protocol.JSONObject
	schemas        []json.RawMessage
	system         json.RawMessage
	fallback       float64
	upper          float64
	previous       json.RawMessage
}

type Result struct {
	Params            *requestmapping.Value
	PreparedRequest   *provider.MessagesRequest
	NormalizedHistory []protocol.JSONObject
	BudgetSource      string
	PreviousRequestID json.RawMessage
}

// CapabilityError is not a counterfeit Source exception. Callers must stop
// instead of approximating a branch whose behavior has not been migrated.
type CapabilityError struct{ Boundary string }

func (failure *CapabilityError) Error() string {
	return fmt.Sprintf("request context capability is not migrated: %s", failure.Boundary)
}
func unsupported(boundary string) error { return &CapabilityError{Boundary: boundary} }
