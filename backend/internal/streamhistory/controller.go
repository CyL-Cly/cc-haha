package streamhistory

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type Options struct {
	UUID           func() string
	Now            func() time.Time
	RequestID      *string
	RefusalProfile *RefusalProfile
}

// RefusalProfile contains already-resolved host values used by Source's
// getErrorMessageIfRefusal. This assembler never reads session/global config.
type RefusalProfile struct {
	Model          string
	NonInteractive bool
}
type sourceFailure struct{ message string }

func (failure *sourceFailure) Error() string { return failure.message }
func sourceError(message string) error       { return &sourceFailure{message: message} }

// Unsupported is an explicit component boundary, not a counterfeit Source
// exception or a promise that transport/retry policies have already migrated.
var ErrUnsupported = errors.New("stream history input is outside the migrated default single-response profile")
var ErrClosed = errors.New("stream history controller has finished")

// Controller and its live envelopes have one owning goroutine, like the Source
// generator. Callers must serialize Feed, Finish and observation/persistence.
type Controller struct {
	options                      Options
	factory                      *messages.Factory
	start                        *protocol.JSONObject
	startUsage                   *Usage
	usage                        *Usage
	blocks                       map[int]*Block
	completed                    map[int]bool
	toolIDs                      map[string]bool
	created                      []*Envelope
	pending                      []*Envelope
	crossed, messageStop, closed bool
	stopReason                   json.RawMessage
	handled                      map[string]bool
}

func New(options Options) (*Controller, error) {
	factory, err := messages.NewFactory(options.UUID, options.Now)
	if err != nil {
		return nil, err
	}
	return &Controller{options: options, factory: factory, usage: &Usage{Raw: json.RawMessage(emptyUsage)}, blocks: map[int]*Block{}, completed: map[int]bool{}, toolIDs: map[string]bool{}, handled: map[string]bool{}, stopReason: json.RawMessage("null")}, nil
}

func (controller *Controller) StartJSON() json.RawMessage {
	if controller.start == nil {
		return nil
	}
	return compact(*controller.start)
}
func (controller *Controller) StartUsage() *Usage { return controller.startUsage }

func indexOf(part protocol.JSONObject) (int, error) {
	var index int
	if err := json.Unmarshal(part.Get("index"), &index); err != nil || index < 0 {
		return 0, ErrUnsupported
	}
	return index, nil
}
func canonical(raw json.RawMessage) string {
	normalized, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
	return string(normalized)
}

func (controller *Controller) envelope(block *Block, partial bool) *Envelope {
	message := controller.start.Clone()
	usage := controller.startUsage
	if partial {
		usage = controller.usage
		message.Set("usage", usage.Raw)
	}
	uuid := controller.options.UUID()
	timestamp := time.UnixMilli(controller.options.Now().UnixMilli()).UTC().Format("2006-01-02T15:04:05.000Z")
	var requestID json.RawMessage
	if controller.options.RequestID != nil {
		requestID = literal(*controller.options.RequestID)
	}
	record := jsonObject(field("message", nil), field("requestId", requestID), field("type", literal("assistant")), field("uuid", literal(uuid)), field("timestamp", literal(timestamp)))
	message.Set("content", json.RawMessage("[]"))
	envelope := &Envelope{Record: record, Message: message, Content: []*Block{block}, Usage: usage, UUID: uuid}
	controller.created = append(controller.created, envelope)
	return envelope
}

func (controller *Controller) drain(withoutTools bool, emit Emit) error {
	pending := controller.pending
	controller.pending = nil
	for _, envelope := range pending {
		if withoutTools && envelope.Content[0].Object.String("type") == "tool_use" {
			continue
		}
		if err := emit(Update{Envelope: envelope}); err != nil {
			if !isYieldThrow(err) {
				controller.closed = true
			}
			return err
		}
	}
	return nil
}
func (controller *Controller) add(envelope *Envelope, emit Emit) error {
	controller.pending = append(controller.pending, envelope)
	if controller.crossed {
		return nil
	}
	typ := envelope.Content[0].Object.String("type")
	if typ != "tool_use" && typ != "server_tool_use" {
		return nil
	}
	controller.crossed = true
	if typ == "tool_use" {
		return nil
	}
	return controller.drain(false, emit)
}

func (controller *Controller) Feed(raw json.RawMessage, timing Timing, emit Emit) error {
	if controller.closed {
		return ErrClosed
	}
	if emit == nil {
		return errors.New("stream history requires an event consumer")
	}
	var part protocol.JSONObject
	if err := json.Unmarshal(raw, &part); err != nil || !part.IsObject() {
		return ErrUnsupported
	}
	switch part.String("type") {
	case "message_start":
		message := object(part.Get("message"))
		if !message.IsObject() {
			return ErrUnsupported
		}
		controller.start = &message
		if usage := message.Get("usage"); usage != nil {
			controller.startUsage = &Usage{Raw: usage}
		} else {
			controller.startUsage = nil
		}
		usage, err := updateUsage(controller.usage, message.Get("usage"))
		if err != nil {
			return err
		}
		controller.usage = usage
	case "content_block_start":
		index, err := indexOf(part)
		if err != nil {
			return err
		}
		block := object(part.Get("content_block"))
		if !block.IsObject() {
			return ErrUnsupported
		}
		switch block.String("type") {
		case "text":
			block.Set("text", literal(""))
		case "thinking":
			block.Set("thinking", literal(""))
			block.Set("signature", literal(""))
		case "tool_use", "server_tool_use":
			block.Set("input", literal(""))
		}
		controller.blocks[index] = &Block{Object: block}
	case "content_block_delta":
		index, err := indexOf(part)
		if err != nil {
			return err
		}
		block := controller.blocks[index]
		if block == nil {
			return sourceError("Content block not found")
		}
		delta := object(part.Get("delta"))
		if !delta.IsObject() {
			return ErrUnsupported
		}
		name, required := "", ""
		switch delta.String("type") {
		case "text_delta":
			name, required = "text", "text"
		case "thinking_delta":
			name, required = "thinking", "thinking"
		case "input_json_delta":
			name, required = "input", "input_json"
		case "signature_delta":
			if block.Object.String("type") != "thinking" {
				return sourceError("Content block is not a thinking block")
			}
			block.Object.Set("signature", delta.Get("signature"))
		}
		if name != "" {
			typ := block.Object.String("type")
			if required == "input_json" && typ != "tool_use" && typ != "server_tool_use" || required != "input_json" && typ != required {
				return sourceError("Content block is not a " + required + " block")
			}
			deltaField := name
			if name == "input" {
				deltaField = "partial_json"
			}
			value, err := protocol.JavaScriptStringJSON(delta.Get(deltaField))
			if err != nil {
				return err
			}
			joined, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{block.Object.Get(name), value}, "")
			if err != nil {
				return err
			}
			block.Object.Set(name, joined)
			if name == "input" && nullish(delta.Get(deltaField)) {
				value := "null"
				if delta.Get(deltaField) == nil {
					value = "undefined"
				}
				// Source += mutates input first; its subsequent .length
				// access throws before emitting the raw delta or allocating
				// this tool's assistant envelope.
				return sourceError(value + " is not an object (evaluating 'delta.partial_json.length')")
			}
		}
	case "content_block_stop":
		index, err := indexOf(part)
		if err != nil {
			return err
		}
		if controller.completed[index] {
			return nil
		}
		controller.completed[index] = true
		block := controller.blocks[index]
		if block == nil {
			return sourceError("Content block not found")
		}
		if controller.start == nil {
			return sourceError("Message not found")
		}
		typ := block.Object.String("type")
		if typ == "tool_use" || typ == "server_tool_use" {
			id := canonical(block.Object.Get("id"))
			if controller.toolIDs[id] {
				return nil
			}
			controller.toolIDs[id] = true
			units, err := protocol.DecodeJavaScriptJSONString(block.Object.Get("input"))
			if err != nil {
				return err
			}
			copy := block.Object.Clone()
			copy.Set("input", protocol.NormalizeToolArgumentUTF16(units, typ == "tool_use"))
			block = &Block{Object: copy}
		}
		if err := controller.add(controller.envelope(block, false), emit); err != nil {
			return err
		}
	case "message_delta":
		delta := object(part.Get("delta"))
		if !delta.IsObject() {
			return ErrUnsupported
		}
		usage, err := updateUsage(controller.usage, part.Get("usage"))
		if err != nil {
			return err
		}
		controller.usage = usage
		reason := delta.Get("stop_reason")
		hasReason := !nullish(reason)
		newReason := hasReason && !controller.handled[canonical(reason)]
		if hasReason {
			controller.stopReason = reason
			controller.handled[canonical(reason)] = true
		}
		if len(controller.created) > 0 {
			last := controller.created[len(controller.created)-1]
			last.Usage = controller.usage
			last.Message.Set("usage", last.Usage.Raw)
			last.Message.Set("stop_reason", controller.stopReason)
			if hasReason {
				last.Message.Set("stop_sequence", coalesce(delta.Get("stop_sequence"), json.RawMessage("null")))
			}
		}
		if newReason && controller.isLimit() {
			truncated := false
			for _, pending := range controller.pending {
				if pending.Content[0].Object.String("type") == "tool_use" {
					truncated = true
				}
			}
			if err := controller.drain(truncated, emit); err != nil {
				return err
			}
			text := ""
			apiError := !truncated
			if canonical(controller.stopReason) == `"max_tokens"` {
				if truncated {
					text = "The model's tool call was truncated at an upstream length limit, so it was not executed. Retry with a larger supported output budget or split the operation into smaller tool calls."
				} else {
					text = "The model's response was truncated at an upstream length limit (output or context). Check the provider's output budget and context limit."
				}
			} else {
				if truncated {
					text = "The model's tool call was truncated at the context window limit, so it was not executed."
				} else {
					text = "The model has reached its context window limit."
				}
			}
			if err := emit(Update{Envelope: controller.synthetic(text, "max_output_tokens", apiError)}); err != nil {
				controller.closed = true
				return err
			}
		}
		if newReason && canonical(reason) == `"refusal"` {
			profile := controller.options.RefusalProfile
			if profile == nil {
				return ErrUnsupported
			}
			text := "Claude Code is unable to respond to this request, which appears to violate our Usage Policy (https://www.anthropic.com/legal/aup). "
			if profile.NonInteractive {
				text += "Try rephrasing the request or attempting a different approach."
			} else {
				text += "Please double press esc to edit your last message or start a new session for Claude Code to assist with a different task."
			}
			if profile.Model != "claude-sonnet-4-20250514" {
				text += " If you are seeing this refusal repeatedly, try running /model claude-sonnet-4-20250514 to switch models."
			}
			// Source constructs this feedback before flushing the provider
			// buffer: allocation must precede the first drained envelope.
			refusal := controller.synthetic(text, "invalid_request", false)
			if err := controller.drain(false, emit); err != nil {
				return err
			}
			if err := emit(Update{Envelope: refusal}); err != nil {
				controller.closed = true
				return err
			}
		}
	case "message_stop":
		controller.messageStop = true
	}
	serialized, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return err
	}
	if err := emit(Update{Event: serialized, Timing: timing}); err != nil {
		if !isYieldThrow(err) {
			controller.closed = true
		}
		return err
	}
	return nil
}

func (controller *Controller) isLimit() bool {
	reason := canonical(controller.stopReason)
	return reason == `"max_tokens"` || reason == `"model_context_window_exceeded"`
}

// synthetic uses the already-ported factory for allocation/default fields,
// then restores the Source factory's known property order at this wire seam.
func (controller *Controller) synthetic(text, code string, apiError bool) *Envelope {
	options := storage.Object{"content": literal("API Error: " + text), "error": literal(code)}
	if apiError {
		options["apiError"] = literal(code)
	}
	value := controller.factory.CreateAssistantAPIError(options)
	api, _, _ := storage.NormalizeObject(value["message"])
	message := jsonObject()
	for _, key := range []string{"id", "container", "model", "role", "stop_reason", "stop_sequence", "type", "usage", "content", "context_management"} {
		message.Set(key, api[key])
	}
	record := jsonObject()
	for _, key := range []string{"type", "uuid", "timestamp", "message", "requestId", "apiError", "error", "errorDetails", "businessErrorCode", "sourceModel", "isApiErrorMessage", "isVirtual"} {
		record.Set(key, value[key])
	}
	var blocks []protocol.JSONObject
	_ = json.Unmarshal(api["content"], &blocks)
	content := make([]*Block, 0, len(blocks))
	for _, block := range blocks {
		content = append(content, &Block{Object: block})
	}
	return &Envelope{Record: record, Message: message, Content: content, Usage: &Usage{Raw: api["usage"]}, UUID: record.String("uuid"), APIError: true}
}

// Abort is the caller-signal branch of the same commit buffer. Keep displayable
// text, including text whose block_stop was never received, and drop pending
// local tools. It neither invents normal completion nor emits an API error.
func (controller *Controller) Abort(emit Emit) error {
	if controller.closed {
		return ErrClosed
	}
	if emit == nil {
		return errors.New("stream history requires an event consumer")
	}
	defer func() { controller.closed = true }()
	controller.preservePartialText()
	return controller.drain(true, emit)
}

func (controller *Controller) preservePartialText() {
	if !controller.isLimit() && controller.start != nil {
		indexes := make([]int, 0, len(controller.blocks))
		for index := range controller.blocks {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			block := controller.blocks[index]
			if block.Object.String("type") == "text" && block.Object.String("text") != "" && !controller.completed[index] {
				controller.completed[index] = true
				// Partial text cannot cross the first tool boundary. Retain
				// it until the completion/error drain, as Source does.
				controller.pending = append(controller.pending, controller.envelope(block, true))
			}
		}
	}
}

func (controller *Controller) Finish(cause error, emit Emit) error {
	if controller.closed {
		return ErrClosed
	}
	if emit == nil {
		return errors.New("stream history requires an event consumer")
	}
	defer func() { controller.closed = true }()
	if cause == nil {
		controller.preservePartialText()
	}
	if cause == nil && !controller.isLimit() {
		if controller.start == nil || len(controller.created) == 0 && (nullish(controller.stopReason) || canonical(controller.stopReason) == `"tool_use"`) {
			cause = sourceError("Stream ended without receiving any events")
		} else {
			incomplete := !controller.messageStop || nullish(controller.stopReason)
			for index := range controller.blocks {
				if !controller.completed[index] {
					incomplete = true
				}
			}
			if incomplete {
				cause = sourceError("Provider stream ended before completing the response")
			}
		}
	}
	if cause == nil {
		return controller.drain(false, emit)
	}
	var source *sourceFailure
	if !errors.As(cause, &source) {
		return fmt.Errorf("%w: failure %v", ErrUnsupported, cause)
	}
	if err := controller.drain(true, emit); err != nil {
		return err
	}
	return emit(Update{Envelope: controller.synthetic(source.message, "unknown", false)})
}
