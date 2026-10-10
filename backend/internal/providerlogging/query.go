// Package providerlogging owns the finite local, unattached analytics queue.
// It does not implement telemetry exporters, sampling, spans or process policy.
package providerlogging

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"strings"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "query logging capability is not migrated: " + e.Boundary
}

type QueryInput struct {
	Model, QuerySource, ThinkingType string
	MessagesLength                   int
	Temperature                      float64
	Betas                            []string
	FastMode                         bool
	PreviousRequestID                json.RawMessage
	Environment                      map[string]string
	// Native captures these from the actual params, retaining UTF-16 and
	// undefined rather than inferring them from requested thinking/effort.
	ThinkingTypeValue, EffortValue, BetasJoined json.RawMessage
	QueryTracking                               json.RawMessage
}
type Event struct {
	Name     string
	Metadata protocol.JSONObject
	Async    bool
}
type Queue struct {
	owner   *cooperative.Owner
	events  []Event
	pending []Event
	sink    func(*cooperative.Scope, Event) error
}

func New(owner *cooperative.Owner) (*Queue, error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	return &Queue{owner: owner}, nil
}
func (q *Queue) Owns(owner *cooperative.Owner) bool {
	return q != nil && owner != nil && q.owner == owner
}
func data(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// FromOriginal uses the sole prepared history and original options, not a
// captured Source request. Effort/beta/build-time variants remain finite gaps.
func FromOriginal(input requestcontext.Context, history []protocol.JSONObject) (QueryInput, error) {
	if strings.Contains(strings.ToLower(input.Options.Model), "[1m]") {
		return QueryInput{}, &CapabilityError{"context-beta model"}
	}
	result := QueryInput{Model: input.Options.Model, QuerySource: input.Options.QuerySource, ThinkingType: input.Thinking.Type, MessagesLength: len(history), Temperature: 1, Betas: append([]string(nil), input.Host.SDKBetas...), Environment: map[string]string{}}
	if input.Options.TemperatureOverride != nil {
		result.Temperature = *input.Options.TemperatureOverride
	}
	for name, value := range input.Host.Env {
		result.Environment[name] = value
	}
	for i := len(input.History) - 1; i >= 0; i-- {
		if input.History[i].String("type") == "assistant" {
			result.PreviousRequestID = input.History[i].Get("requestId")
			break
		}
	}
	return result, nil
}
func (q *Queue) LogQuery(scope *cooperative.Scope, input QueryInput, mode json.RawMessage) error {
	if q == nil || !scope.BelongsTo(q.owner) {
		return cooperative.ErrRestricted
	}
	// Admitted hosts currently resolve direct firstParty. Do not silently log a
	// false provider for unported cloud/managed environment selection.
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if value := input.Environment[name]; value != "" && value != "0" && value != "false" {
			return &CapabilityError{"active cloud provider"}
		}
	}
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: data(input.Model)}, protocol.JSONField{Name: "messagesLength", Value: data(input.MessagesLength)}, protocol.JSONField{Name: "temperature", Value: data(input.Temperature)}, protocol.JSONField{Name: "provider", Value: data("firstParty")}, protocol.JSONField{Name: "buildAgeMins", Value: nil})
	if input.BetasJoined != nil {
		metadata.Set("betas", input.BetasJoined)
	} else if len(input.Betas) > 0 {
		metadata.Set("betas", data(strings.Join(input.Betas, ",")))
	}
	metadata.Set("permissionMode", mode)
	metadata.Set("querySource", data(input.QuerySource))
	if input.QueryTracking != nil {
		var tracking protocol.JSONObject
		if err := tracking.UnmarshalJSON(input.QueryTracking); err != nil || !tracking.IsObject() {
			return &CapabilityError{"original query tracking object"}
		}
		metadata.Set("queryChainId", tracking.Get("chainId"))
		metadata.Set("queryDepth", tracking.Get("depth"))
	}
	thinkingType := input.ThinkingTypeValue
	if thinkingType == nil {
		thinkingType = data(input.ThinkingType)
	}
	metadata.Set("thinkingType", thinkingType)
	metadata.Set("effortValue", input.EffortValue)
	metadata.Set("fastMode", data(input.FastMode))
	if value := input.PreviousRequestID; len(value) > 0 && string(value) != "null" && string(value) != "\"\"" {
		metadata.Set("previousRequestId", value)
	}
	for _, pair := range [][2]string{{"ANTHROPIC_BASE_URL", "baseUrl"}, {"ANTHROPIC_MODEL", "envModel"}, {"ANTHROPIC_SMALL_FAST_MODEL", "envSmallFastModel"}} {
		if value := input.Environment[pair[0]]; value != "" {
			metadata.Set(pair[1], data(value))
		}
	}
	event := Event{Name: "tengu_api_query", Metadata: metadata, Async: false}
	q.events = append(q.events, event)
	if q.sink == nil {
		q.pending = append(q.pending, event)
		return nil
	}
	return q.sink(scope, event)
}
func (q *Queue) Events(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	if q == nil || !scope.BelongsTo(q.owner) {
		return nil, cooperative.ErrRestricted
	}
	out := make([]protocol.JSONObject, len(q.events))
	for i, event := range q.events {
		out[i] = protocol.NewJSONObject(protocol.JSONField{Name: "eventName", Value: data(event.Name)}, protocol.JSONField{Name: "metadata", Value: data(event.Metadata)}, protocol.JSONField{Name: "async", Value: data(event.Async)})
	}
	return out, nil
}

// OriginalEvents retains undefined own properties in metadata while Events
// remains the persisted JSON projection. Observers receive independent clones.
func (q *Queue) OriginalEvents(scope *cooperative.Scope) ([]Event, error) {
	if q == nil || !scope.BelongsTo(q.owner) {
		return nil, cooperative.ErrRestricted
	}
	out := make([]Event, len(q.events))
	for i, event := range q.events {
		out[i] = Event{Name: event.Name, Metadata: event.Metadata.Clone(), Async: event.Async}
	}
	return out, nil
}

// AttachSink ports the finite synchronous-event branch of analytics sink
// attachment. Queued events drain on the existing owner's microtask; already
// attached sinks are unchanged. Async events/exporters remain outside this API.
func (q *Queue) AttachSink(scope *cooperative.Scope, sink func(*cooperative.Scope, Event) error) error {
	if q == nil || !scope.BelongsTo(q.owner) || sink == nil {
		return cooperative.ErrRestricted
	}
	if q.sink != nil {
		return nil
	}
	q.sink = sink
	if len(q.pending) == 0 {
		return nil
	}
	queued := append([]Event(nil), q.pending...)
	q.pending = nil
	_, err := q.owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
		for _, event := range queued {
			if err := q.sink(next, event); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}
