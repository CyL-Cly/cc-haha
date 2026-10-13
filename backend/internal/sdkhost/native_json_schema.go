package sdkhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/jsonschemavalidator"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// NativeJSONSchema binds the real public CLI schema to the original transient
// tool and pinned Ajv primitive. It cannot install a tool in a foreign owner.
type NativeJSONSchema struct {
	owner     *cooperative.Owner
	resources *ownerasync.NativeBinding
	validator *jsonschemavalidator.Validator
	synthetic *NativeSyntheticOutput
	turn      *nativeJSONSchemaTurn
}
type nativeJSONSchemaTurn struct {
	initialCalls int
	output       json.RawMessage
}

func NewNativeJSONSchema(owner *cooperative.Owner, resources *ownerasync.NativeBinding, schema json.RawMessage) (*NativeJSONSchema, error) {
	if owner == nil || resources == nil || !resources.Owns(owner) || !json.Valid(schema) {
		return nil, cooperative.ErrRestricted
	}
	trimmed := strings.TrimSpace(string(schema))
	// Source truthiness precedes tool construction. Falsy parsed CLI values do
	// not activate a schema, while primitives cannot enter its WeakMap cache.
	if trimmed == "null" || trimmed == "false" || trimmed == "0" || trimmed == `""` {
		return nil, nil
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return nil, errors.New("Invalid value used as weak map key")
	}
	compiler, e := jsonschemavalidator.New()
	if e != nil {
		return nil, e
	}
	validator, e := compiler.Compile(context.Background(), 1, schema)
	if e != nil {
		return nil, e
	}
	b := &NativeJSONSchema{owner: owner, resources: resources, validator: validator}
	if validator.BuildError() != "" {
		return b, nil
	}
	b.synthetic, e = NewNativeSyntheticOutput(owner, resources, schema, validator.Validate)
	return b, e
}
func (b *NativeJSONSchema) Owns(owner *cooperative.Owner) bool {
	return b != nil && b.owner == owner && b.resources != nil && b.resources.Owns(owner) && b.validator != nil
}
func (b *NativeJSONSchema) OwnsPublicJSONSchema(owner *cooperative.Owner) bool {
	return b.Owns(owner) && b.synthetic != nil && b.synthetic.Owns(owner) && b.validator.BuildError() == ""
}
func (b *NativeJSONSchema) SyntheticToolNames(scope *cooperative.Scope) ([]string, error) {
	if b == nil || !scope.BelongsTo(b.owner) || !b.OwnsPublicJSONSchema(b.owner) {
		return nil, cooperative.ErrRestricted
	}
	return b.synthetic.SyntheticToolNames(scope)
}
func (b *NativeJSONSchema) Synthetic() *NativeSyntheticOutput {
	if b == nil {
		return nil
	}
	return b.synthetic
}
func (b *NativeJSONSchema) BuildError() string {
	if b == nil {
		return ""
	}
	return b.validator.BuildError()
}

func structuredCalls(messages []queryloop.Message) int {
	count := 0
	for _, message := range messages {
		if message.Type() != "assistant" {
			continue
		}
		data, _ := message.MarshalJSON()
		api := object(object(data).Get("message"))
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(api.Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") == "tool_use" && block.String("name") == "StructuredOutput" {
				count++
				break
			}
		}
	}
	return count
}
func (h *Host) beginNativeJSONSchemaTurn(scope *cooperative.Scope, history []queryloop.Message) (*nativeJSONSchemaTurn, error) {
	if h.options.Print == nil || h.options.Print.NativeJSONSchema == nil {
		return nil, nil
	}
	b := h.options.Print.NativeJSONSchema
	if h.isAgent() || !scope.BelongsTo(h.options.Owner) || !b.Owns(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	initial := structuredCalls(history)
	b.turn = &nativeJSONSchemaTurn{initialCalls: initial}
	return b.turn, nil
}
func (h *Host) observeNativeJSONSchemaYield(scope *cooperative.Scope, turn *nativeJSONSchemaTurn, message queryloop.Message) error {
	if turn == nil {
		return nil
	}
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	if message.Type() == "attachment" {
		a := object(message.Record.Get("attachment"))
		if a.String("type") == "structured_output" {
			turn.output = append(json.RawMessage(nil), a.Get("data")...)
		}
	}
	return nil
}

var structuredRetryInteger = regexp.MustCompile(`^[+-]?\d+`)

func structuredRetryLimit(value string) float64 {
	if value == "" {
		value = "5"
	}
	match := structuredRetryInteger.FindString(protocol.TrimJavaScriptString(value))
	if match == "" {
		return math.NaN()
	}
	n, e := strconv.ParseFloat(match, 64)
	if e != nil && !errors.Is(e, strconv.ErrRange) {
		return math.NaN()
	}
	return n
}
func (h *Host) nativeJSONSchemaRetryLimit(scope *cooperative.Scope, turn *nativeJSONSchemaTurn, message queryloop.Message, usage protocol.JSONObject, lastStop json.RawMessage, start float64, turnCount int, checkpoint func() error) (bool, error) {
	if turn == nil || message.Type() != "user" {
		return false, nil
	}
	limit := structuredRetryLimit(h.options.OriginalEnv["MAX_STRUCTURED_OUTPUT_RETRIES"])
	// Read current mutable history, including compaction/replacement, exactly
	// where Source counts after recording and yielding this user message.
	if math.IsNaN(limit) || float64(structuredCalls(h.serial.History())-turn.initialCalls) < limit {
		return false, nil
	}
	if e := checkpoint(); e != nil {
		return true, e
	}
	snapshot, e := h.sessionAccounting.Snapshot(scope)
	if e != nil {
		return true, e
	}
	duration, e := h.sessionAccounting.DurationSnapshot(scope)
	if e != nil {
		return true, e
	}
	result := protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: quote("result")}, protocol.JSONField{Name: "subtype", Value: quote("error_max_structured_output_retries")},
		protocol.JSONField{Name: "duration_ms", Value: raw(h.options.NowMillis() - start)}, protocol.JSONField{Name: "duration_api_ms", Value: duration.Get("totalAPIDuration")},
		protocol.JSONField{Name: "is_error", Value: raw(true)}, protocol.JSONField{Name: "num_turns", Value: raw(turnCount)}, protocol.JSONField{Name: "stop_reason", Value: lastStop},
		protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())}, protocol.JSONField{Name: "total_cost_usd", Value: snapshot.Get("totalCostUSD")},
		protocol.JSONField{Name: "usage", Value: raw(usage)}, protocol.JSONField{Name: "modelUsage", Value: snapshot.Get("modelUsage")},
		protocol.JSONField{Name: "permission_denials", Value: h.permissionDenialsJSON()}, protocol.JSONField{Name: "fast_mode_state", Value: quote("off")}, protocol.JSONField{Name: "uuid", Value: quote(h.options.UUID())},
		protocol.JSONField{Name: "errors", Value: raw([]string{fmt.Sprintf("Failed to provide valid structured output after %s attempts", strconv.FormatFloat(limit, 'f', -1, 64))})},
	)
	_, e = h.enqueue(scope, result)
	return true, e
}
func latestStructuredCallSucceeded(messages []queryloop.Message) bool {
	id := ""
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Type() != "assistant" {
			continue
		}
		data, _ := m.MarshalJSON()
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(object(object(data).Get("message")).Get("content"), &blocks)
		for _, b := range blocks {
			if b.String("type") == "tool_use" && b.String("name") == "StructuredOutput" {
				id = b.String("id")
				break
			}
		}
		if id != "" {
			break
		}
	}
	if id == "" {
		return false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Type() != "user" {
			continue
		}
		data, _ := m.MarshalJSON()
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(object(object(data).Get("message")).Get("content"), &blocks)
		for _, b := range blocks {
			if b.String("type") == "tool_result" && b.String("tool_use_id") == id {
				return string(b.Get("is_error")) != "true"
			}
		}
	}
	return false
}
func (h *Host) nativeJSONSchemaStop(scope *cooperative.Scope, round queryloop.Round, assistants []*streamhistory.Envelope, decision *queryloop.StopDecision, emits ...queryloop.Emit) error {
	if h.options.Print == nil || h.options.Print.NativeJSONSchema == nil || !h.options.Print.NativeJSONSchema.OwnsPublicJSONSchema(h.options.Owner) || h.isAgent() {
		return nil
	}
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	// Original function hooks share SIMPLE/trust/Stop gates and run after
	// existing hooks. Their latest successful call check spans full history.
	if nativeJSONSchemaTruthyEnv(h.options.OriginalEnv["CLAUDE_CODE_SIMPLE"]) || decision.PreventContinuation {
		return nil
	}
	history := append([]queryloop.Message{}, round.History...)
	for _, a := range assistants {
		history = append(history, queryloop.Message{Assistant: a})
	}
	if latestStructuredCallSucceeded(history) {
		return nil
	}
	if len(emits) != 1 || emits[0] == nil {
		return cooperative.ErrRestricted
	}
	record := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": quote("Stop hook feedback:\n" + queryhooks.AgentStructuredOutputFeedback), "isMeta": raw(true)}))
	message := queryloop.Message{Record: record}
	decision.BlockingMessages = append(decision.BlockingMessages, message)
	decision.AllowBlockingContinuation = true
	return emits[0](queryloop.Yield{Message: &message})
}

// Keep the actual current-query attachment absent on errors and later turns.
func (h *Host) nativeJSONSchemaResult(result *protocol.JSONObject) {
	if result.String("subtype") != "success" || h.options.Print == nil {
		return
	}
	b := h.options.Print.NativeJSONSchema
	if b != nil && b.turn != nil && len(b.turn.output) > 0 {
		result.Set("structured_output", b.turn.output)
	}
}

func nativeJSONSchemaTruthyEnv(value string) bool {
	switch strings.ToLower(protocol.TrimJavaScriptString(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
