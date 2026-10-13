// Package toolschema renders migrated original tool definitions. It does not
// accept a Source-rendered Anthropic schema as a replacement for native tools.
package toolschema

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Field struct {
	Name   string `json:"name"`
	Schema Schema `json:"schema"`
}
type Schema struct {
	Kind             string          `json:"kind"`
	UnknownKeys      string          `json:"unknownKeys,omitempty"`
	Fields           []Field         `json:"fields,omitempty"`
	Optional         bool            `json:"optional,omitempty"`
	Description      json.RawMessage `json:"description,omitempty"`
	Default          json.RawMessage `json:"default,omitempty"`
	Minimum          *float64        `json:"minimum,omitempty"`
	ExclusiveMinimum *float64        `json:"exclusiveMinimum,omitempty"`
	Integer          bool            `json:"integer,omitempty"`
	Values           []string        `json:"values,omitempty"`
	Semantic         bool            `json:"semantic,omitempty"`
	// These native recipe nodes are not accepted in registered declarations.
	Format        string          `json:"-"`
	Native        bool            `json:"-"`
	Items         *Schema         `json:"-"`
	MinimumLength *int            `json:"-"`
	MaximumLength *int            `json:"-"`
	MinimumItems  *int            `json:"-"`
	Maximum       *float64        `json:"-"`
	MaximumItems  *int            `json:"-"`
	RecordValues  *Schema         `json:"-"`
	Members       []Schema        `json:"-"`
	Discriminator string          `json:"-"`
	Literal       json.RawMessage `json:"-"`
	Pattern       string          `json:"-"`
}
type Definition struct {
	IsMCP           bool            `json:"-"`
	AlwaysLoad      bool            `json:"-"`
	ShouldDefer     bool            `json:"-"`
	SearchHint      json.RawMessage `json:"-"`
	Kind            string          `json:"kind"`
	Name            string          `json:"name"`
	InputSchema     *Schema         `json:"inputSchema,omitempty"`
	InputJSONSchema json.RawMessage `json:"inputJSONSchema,omitempty"`
	Description     json.RawMessage `json:"description,omitempty"`
	Prompt          json.RawMessage `json:"prompt,omitempty"`
	Strict          bool            `json:"strict,omitempty"`
	Aliases         []string        `json:"aliases,omitempty"`
}
type Host struct {
	ToolSearchDeltaEnabled   bool
	BaseURL                  string
	MainLoopModel            string
	CompactLinePrefixKilled  bool
	ReadLimits               json.RawMessage // Original feature-store configuration.
	StrictEnabled            bool
	FGTSEnabled              bool
	DisableExperimentalBetas bool
	BashEnvironment          map[string]string
	PowerShellEdition        string
	AgentDefinitions         []AgentPromptDefinition
	QuestionPreviewFormat    string
	PlanInterviewPhase       bool
	PlanUserType             string
	WebSearchMonthYear       string
	WorkflowConcurrency      int
	WorkflowSizeGuideline    string
	TeamReviewRequired       bool
	TeamEnabled              bool
}
type AgentPromptDefinition struct {
	Type, WhenToUse        string
	Tools, DisallowedTools []string
}
type Options struct {
	Host         Host
	Model        string
	DeferLoading bool
	CacheControl json.RawMessage
}
type CapabilityError struct{ Boundary string }

func (failure *CapabilityError) Error() string {
	return "tool schema capability is not migrated: " + failure.Boundary
}
func unsupported(boundary string) error { return &CapabilityError{Boundary: boundary} }

type ValidationError struct{ Issues json.RawMessage }

func (failure *ValidationError) Error() string { return fmt.Sprintf("%s", failure.Issues) }

type readPromptLimits struct {
	MaxSize               float64
	IncludeSize, Targeted bool
}
type Session struct {
	mu          sync.Mutex
	cache       map[string]protocol.JSONObject
	promptCalls map[string]int
	readLimits  *readPromptLimits
}

func NewSession() *Session {
	return &Session{cache: map[string]protocol.JSONObject{}, promptCalls: map[string]int{}}
}
func (session *Session) Clear() { session.mu.Lock(); defer session.mu.Unlock(); clear(session.cache) }
func (session *Session) CacheSize() int {
	session.mu.Lock()
	defer session.mu.Unlock()
	return len(session.cache)
}
func (session *Session) PromptCalls() map[string]int {
	session.mu.Lock()
	defer session.mu.Unlock()
	result := map[string]int{}
	for name, count := range session.promptCalls {
		result[name] = count
	}
	return result
}
