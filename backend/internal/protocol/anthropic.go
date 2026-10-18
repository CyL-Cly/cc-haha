package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Message and ContentBlock retain provider fields and JSON numbers verbatim.
// SDK envelope UUIDs and parent_tool_use_id belong to the session controller;
// the provider's message.id and tool block.id must never be regenerated here.
type Message map[string]json.RawMessage
type ContentBlock = JSONObject
type Usage map[string]json.RawMessage

func (message Message) String(field string) string { return stringField(message, field) }
func (block ContentBlock) String(field string) string {
	var value string
	_ = json.Unmarshal(block.Get(field), &value)
	return value
}

func (message Message) Blocks() []ContentBlock {
	var blocks []ContentBlock
	_ = json.Unmarshal(message["content"], &blocks)
	return blocks
}

func (message Message) Usage() Usage {
	var usage Usage
	_ = json.Unmarshal(message["usage"], &usage)
	return usage
}

func (message Message) Text() string {
	var text strings.Builder
	for _, block := range message.Blocks() {
		if block.String("type") == "text" {
			text.WriteString(block.String("text"))
		}
	}
	return text.String()
}

// Int64 returns a usage counter without converting it through float64.
func (usage Usage) Int64(field string) int64 {
	value, _ := strconv.ParseInt(string(usage[field]), 10, 64)
	return value
}

// Input is a copy so a caller cannot mutate the committed provider record.
func (block ContentBlock) Input() json.RawMessage {
	return block.Get("input")
}

// StreamUpdate.Event is the original JSON SSE payload, including unknown
// fields. Message is a completed assistant response, or safe display content
// from a failed response. Only a successful terminal response contains local
// tool_use blocks. Raw events may contain partial tools for UI rendering and
// must never be used as execution instructions.
type StreamUpdate struct {
	Event   json.RawMessage
	Message *Message
}

type StreamErrorCode string

const (
	StreamIncomplete    StreamErrorCode = "incomplete_stream"
	StreamOutputLimit   StreamErrorCode = "output_limit"
	StreamProviderError StreamErrorCode = "provider_error"
	StreamInvalidEvent  StreamErrorCode = "invalid_event"
	StreamTransport     StreamErrorCode = "transport_error"
	StreamAborted       StreamErrorCode = "aborted"
)

// StreamError keeps provider errors intact and separates length/context limits
// from EOF, cancellation, and malformed input. The caller owns retry policy.
// TruncatedToolUse distinguishes tool truncation from ordinary text recovery,
// as commitOutputLimitResponse does in the TS runtime.
type StreamError struct {
	Code             StreamErrorCode
	Detail           string
	StopReason       string
	TruncatedToolUse bool
	ProviderError    json.RawMessage
	Cause            error
}

func (err *StreamError) Error() string {
	if err.Detail != "" {
		return err.Detail
	}
	return string(err.Code)
}

func (err *StreamError) Unwrap() error { return err.Cause }

type blockState struct {
	block     ContentBlock
	input     []uint16
	completed bool
}

type finishedBlock struct {
	index int
	block ContentBlock
}

type anthropicState struct {
	message       Message
	usage         Usage
	blocks        map[int]*blockState
	completed     []finishedBlock
	toolIDs       map[string]bool
	messageStop   bool
	stopTerminal  bool
	hasStopReason bool
	stopReason    string
	limitReason   string
}

// DecodeAnthropicStream reads Anthropic Messages SSE and commits tools once,
// after an explicit terminal reason and either message_stop or that reason
// remaining the last content-bearing event. Incomplete EOF, errors and limits
// do not expose executable local tools.
//
// The returned partial message and callback's Message retain completed
// thinking/server blocks and visible text on failure; incomplete thinking and
// local tools are excluded. Empty end_turn responses are valid. Output-limit
// responses may omit message_stop, matching the existing TS recovery path.
//
// Context is checked between reads. For cancellation of a blocked reader,
// the caller must tie its HTTP request/body lifecycle to the same context.
func DecodeAnthropicStream(ctx context.Context, reader io.Reader, emit func(StreamUpdate) error) (*Message, error) {
	state := anthropicState{blocks: make(map[int]*blockState), toolIDs: make(map[string]bool)}
	decoder := NewSSEDecoder(reader)
	for {
		if err := ctx.Err(); err != nil {
			return state.finish(&StreamError{Code: StreamAborted, Detail: err.Error(), Cause: err}, emit)
		}
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return state.finish(&StreamError{Code: StreamAborted, Detail: ctxErr.Error(), Cause: ctxErr}, emit)
			}
			return state.finish(&StreamError{Code: StreamTransport, Detail: err.Error(), Cause: err}, emit)
		}
		var part map[string]json.RawMessage
		if err := json.Unmarshal([]byte(event.Data), &part); err != nil || part == nil {
			return state.finish(invalidEvent("Provider SSE contains malformed JSON", err), emit)
		}
		if emit != nil {
			if err := emit(StreamUpdate{Event: append(json.RawMessage(nil), event.Data...)}); err != nil {
				// A consumer failure is not a provider failure and must not be
				// followed by further callbacks or executable assistant content.
				return state.snapshot(false), err
			}
		}
		if stringField(part, "type") == "error" || event.Event == "error" {
			payload := part["error"]
			if len(payload) == 0 {
				payload = json.RawMessage(event.Data)
			}
			var provider map[string]json.RawMessage
			_ = json.Unmarshal(payload, &provider)
			detail := stringField(provider, "message")
			if detail == "" {
				detail = "Provider reported a stream error"
			}
			return state.finish(&StreamError{Code: StreamProviderError, Detail: detail, ProviderError: append(json.RawMessage(nil), payload...)}, emit)
		}
		if err := state.apply(part); err != nil {
			return state.finish(err, emit)
		}
	}
	if err := ctx.Err(); err != nil {
		return state.finish(&StreamError{Code: StreamAborted, Detail: err.Error(), Cause: err}, emit)
	}
	if state.limitReason != "" {
		return state.finish(&StreamError{Code: StreamOutputLimit, Detail: "Provider response was truncated at an output or context limit", StopReason: state.limitReason, TruncatedToolUse: state.hasToolUse()}, emit)
	}
	if state.message == nil || (!state.messageStop && !state.stopTerminal) || !state.hasStopReason || state.hasIncompleteBlocks() || (state.stopReason == "tool_use" && len(state.completed) == 0) {
		return state.finish(&StreamError{Code: StreamIncomplete, Detail: "Provider stream ended before completing the response", StopReason: state.stopReason}, emit)
	}
	return state.finish(nil, emit)
}

func (state *anthropicState) apply(part map[string]json.RawMessage) error {
	typ := stringField(part, "type")
	switch typ {
	case "message_start":
		if state.message != nil {
			return invalidEvent("Provider stream contains a second message_start", nil)
		}
		if err := json.Unmarshal(part["message"], &state.message); err != nil || state.message == nil {
			return invalidEvent("Provider message_start has no message object", err)
		}
		// Keep the complete message_start usage object, including explicit
		// zero counters and extension/null fields. Later delta updates are
		// cumulative and have field-specific null/zero rules.
		state.usage = Usage(cloneObject(state.message.Usage()))
		state.message["stop_reason"] = json.RawMessage("null")
		state.message["stop_sequence"] = json.RawMessage("null")
	case "content_block_start":
		state.stopTerminal = false
		index, err := eventIndex(part)
		if err != nil {
			return err
		}
		if state.message == nil {
			return invalidEvent("Content block arrived before message_start", nil)
		}
		if prior := state.blocks[index]; prior != nil {
			if prior.completed {
				return nil // A replay of an already-completed index is inert.
			}
			return invalidEvent("Content block was started twice before completing", nil)
		}
		var block ContentBlock
		if err := json.Unmarshal(part["content_block"], &block); err != nil || !block.IsObject() || block.String("type") == "" {
			return invalidEvent("content_block_start has no content block", err)
		}
		switch block.String("type") {
		case "text":
			// TS discards start.text because some SDKs duplicate it in
			// text_delta. Do not silently change that compatibility rule.
			block.Set("text", json.RawMessage(`""`))
		case "thinking":
			block.Set("thinking", json.RawMessage(`""`))
			block.Set("signature", json.RawMessage(`""`))
		case "tool_use", "server_tool_use":
			if block.String("id") == "" {
				return invalidEvent("Tool content block has no identity", nil)
			}
			block.Set("input", json.RawMessage(`""`))
		}
		state.blocks[index] = &blockState{block: block}
	case "content_block_delta":
		state.stopTerminal = false
		index, err := eventIndex(part)
		if err != nil {
			return err
		}
		block := state.blocks[index]
		if block == nil {
			return invalidEvent("Content block not found", nil)
		}
		if block.completed {
			return nil
		}
		var delta map[string]json.RawMessage
		if err := json.Unmarshal(part["delta"], &delta); err != nil || delta == nil {
			return invalidEvent("Content block delta has no delta object", err)
		}
		switch stringField(delta, "type") {
		case "input_json_delta":
			if block.block.String("type") != "tool_use" && block.block.String("type") != "server_tool_use" {
				return invalidEvent("Content block is not a input_json block", nil)
			}
			if raw := delta["partial_json"]; len(raw) == 0 || string(raw) == "null" {
				// The reference concatenates the value, then accesses its
				// length. Missing/null partial_json throws at that point;
				// non-string non-null values are coerced by += instead.
				return invalidEvent("partial_json must not be null or missing", nil)
			}
			if raw := delta["partial_json"]; len(raw) > 0 && raw[0] == '"' {
				block.input = append(block.input, decodeJSString(raw)...)
			} else {
				block.input = append(block.input, deltaStringUnits(delta, "partial_json")...)
			}
		case "text_delta":
			if err := appendBlockString(&block.block, "text", "text", delta, "text"); err != nil {
				return err
			}
		case "thinking_delta":
			if err := appendBlockString(&block.block, "thinking", "thinking", delta, "thinking"); err != nil {
				return err
			}
		case "signature_delta":
			if block.block.String("type") != "thinking" {
				return invalidEvent("Content block is not a thinking block", nil)
			}
			// The TS API replaces the signature, rather than appending it,
			// without a runtime type check. An omitted property becomes
			// undefined and is omitted from serialized assistant content.
			if signature, exists := delta["signature"]; exists {
				block.block.Set("signature", signature)
			} else {
				// Assignment to undefined keeps its property slot for a later
				// signature_delta, even though JSON.stringify omits it now.
				block.block.Set("signature", nil)
			}
			// citations_delta and future deltas are retained in raw events.
			// The existing external TS build does not project them into content.
		}
	case "content_block_stop":
		state.stopTerminal = false
		index, err := eventIndex(part)
		if err != nil {
			return err
		}
		block := state.blocks[index]
		if block == nil {
			return invalidEvent("Content block not found", nil)
		}
		if block.completed {
			return nil
		}
		block.completed = true
		if block.block.String("type") == "tool_use" || block.block.String("type") == "server_tool_use" {
			// Match the source Set's UTF-16 identity. Decoding into a Go
			// string replaces lone surrogates and collapses distinct IDs.
			id := quoteUTF16(decodeJSString(block.block.Get("id")))
			if state.toolIDs[id] {
				return nil
			}
			state.toolIDs[id] = true
			block.block.Set("input", normalizeToolArgumentUnits(block.input, block.block.String("type") == "tool_use"))
		}
		state.completed = append(state.completed, finishedBlock{index: index, block: cloneBlock(block.block)})
	case "message_delta":
		if state.message == nil {
			return invalidEvent("message_delta arrived before message_start", nil)
		}
		var delta map[string]json.RawMessage
		if err := json.Unmarshal(part["delta"], &delta); err != nil || delta == nil {
			return invalidEvent("message_delta has no delta object", err)
		}
		var usage Usage
		if raw := part["usage"]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &usage); err != nil {
				return invalidEvent("message_delta usage is not an object", err)
			}
		}
		state.usage = mergeUsage(state.usage, usage)
		if raw := delta["stop_reason"]; len(raw) > 0 && string(raw) != "null" {
			// The existing raw Stream checks non-null presence, not schema
			// type or truthiness. An empty or future typed reason is still
			// a terminal; retain its original JSON in the provider message.
			reason := stringField(delta, "stop_reason")
			state.hasStopReason = true
			state.stopTerminal = true
			state.stopReason = reason
			state.message["stop_reason"] = append(json.RawMessage(nil), raw...)
			state.message["stop_sequence"] = json.RawMessage("null")
			if sequence, exists := delta["stop_sequence"]; exists {
				state.message["stop_sequence"] = append(json.RawMessage(nil), sequence...)
			}
			if state.limitReason == "" && (reason == "max_tokens" || reason == "model_context_window_exceeded") {
				state.limitReason = reason
			}
		}
	case "message_stop":
		state.messageStop = true
	case "":
		return invalidEvent("Provider event has no type", nil)
	}
	return nil
}

func (state *anthropicState) hasToolUse() bool {
	for _, block := range state.blocks {
		if block.block.String("type") == "tool_use" {
			return true
		}
	}
	return false
}

func (state *anthropicState) hasIncompleteBlocks() bool {
	for _, block := range state.blocks {
		if block.completed {
			continue
		}
		// preservePartialText in TS completes nonempty text on EOF. It
		// never manufactures a completed tool or unsigned thinking block.
		if block.block.String("type") == "text" && block.block.String("text") != "" {
			continue
		}
		return true
	}
	return false
}

func (state *anthropicState) snapshot(success bool) *Message {
	if state.message == nil {
		return nil
	}
	message := Message(cloneObject(state.message))
	blocks := make([]ContentBlock, 0, len(state.completed))
	for _, completed := range state.completed {
		if !success && completed.block.String("type") == "tool_use" {
			continue
		}
		blocks = append(blocks, cloneBlock(completed.block))
	}
	if state.limitReason == "" {
		// preservePartialText runs after all completed blocks, in index
		// order, matching the TS API stream's EOF/error preservation.
		var indexes []int
		for index, block := range state.blocks {
			if !block.completed && block.block.String("type") == "text" && block.block.String("text") != "" {
				indexes = append(indexes, index)
			}
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			blocks = append(blocks, cloneBlock(state.blocks[index].block))
		}
	}
	message["content"], _ = json.Marshal(blocks)
	message["usage"], _ = json.Marshal(state.usage)
	return &message
}

func (state *anthropicState) finish(err error, emit func(StreamUpdate) error) (*Message, error) {
	message := state.snapshot(err == nil)
	if emit != nil && message != nil && (err == nil || len(message.Blocks()) > 0) {
		if emitErr := emit(StreamUpdate{Message: message}); emitErr != nil {
			return message, errors.Join(err, emitErr)
		}
	}
	return message, err
}

func invalidEvent(detail string, cause error) *StreamError {
	return &StreamError{Code: StreamInvalidEvent, Detail: detail, Cause: cause}
}

func eventIndex(part map[string]json.RawMessage) (int, error) {
	var index int
	raw := part["index"]
	if len(raw) == 0 || string(raw) == "null" {
		return 0, invalidEvent("Content block index must be a nonnegative integer", nil)
	}
	if err := json.Unmarshal(raw, &index); err != nil || index < 0 {
		return 0, invalidEvent("Content block index must be a nonnegative integer", err)
	}
	return index, nil
}

func stringField(object map[string]json.RawMessage, field string) string {
	var value string
	_ = json.Unmarshal(object[field], &value)
	return value
}

func appendBlockString(block *ContentBlock, blockType, field string, delta map[string]json.RawMessage, deltaField string) error {
	if block.String("type") != blockType {
		return invalidEvent(fmt.Sprintf("Content block is not a %s block", blockType), nil)
	}
	// Preserve the current TS += coercion, including malformed proxy fields:
	// missing -> "undefined", null -> "null", and numbers/arrays/objects.
	units := decodeJSString(block.Get(field))
	units = append(units, deltaStringUnits(delta, deltaField)...)
	block.Set(field, json.RawMessage(quoteUTF16(units)))
	return nil
}

func cloneObject(object map[string]json.RawMessage) map[string]json.RawMessage {
	copy := make(map[string]json.RawMessage, len(object))
	for key, value := range object {
		copy[key] = append(json.RawMessage(nil), value...)
	}
	return copy
}

func cloneBlock(block ContentBlock) ContentBlock { return block.Clone() }

func normalizeToolArguments(raw string, local bool) json.RawMessage {
	return normalizeToolArgumentUnits(utf16.Encode([]rune(raw)), local)
}

func normalizeToolArgumentUnits(units []uint16, local bool) json.RawMessage {
	raw := jsonSourceUTF16(units)
	if len(units) == 0 || strings.TrimSpace(raw) == "null" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(raw)) {
		// The TS normalizer calls JSON.parse before the assistant message
		// is serialized. Preserve its double-number and UTF-16 behavior;
		// returning the original raw JSON would silently change >2^53
		// tool values, Infinity/underflow, and negative zero.
		return normalizeJSJSON(raw)
	}
	if !local {
		// server_tool_use uses safeParseJSON(input) ?? {} in TS. Only
		// local tools require the non-executable malformed-input marker.
		return json.RawMessage("{}")
	}
	return CreateUnparsedToolInput(units)
}

func quoteUTF16(units []uint16) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	for index := 0; index < len(units); index++ {
		unit := units[index]
		if utf16.IsSurrogate(rune(unit)) {
			if unit >= 0xd800 && unit <= 0xdbff && index+1 < len(units) && units[index+1] >= 0xdc00 && units[index+1] <= 0xdfff {
				quoted.WriteRune(utf16.DecodeRune(rune(unit), rune(units[index+1])))
				index++
			} else {
				fmt.Fprintf(&quoted, `\u%04x`, unit)
			}
			continue
		}
		switch unit {
		case '"', '\\':
			quoted.WriteByte('\\')
			quoted.WriteByte(byte(unit))
		case '\b':
			quoted.WriteString(`\b`)
		case '\f':
			quoted.WriteString(`\f`)
		case '\n':
			quoted.WriteString(`\n`)
		case '\r':
			quoted.WriteString(`\r`)
		case '\t':
			quoted.WriteString(`\t`)
		default:
			if unit < 0x20 {
				fmt.Fprintf(&quoted, `\u%04x`, unit)
			} else {
				quoted.WriteRune(rune(unit))
			}
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}

func mergeUsage(previous, incoming Usage) Usage {
	usage := Usage(cloneObject(previous))
	for key, value := range incoming {
		switch key {
		case "input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens":
			var number json.Number
			if err := json.Unmarshal(value, &number); err != nil {
				continue
			}
			count, err := number.Float64()
			if err != nil || count <= 0 {
				continue
			}
		case "output_tokens", "web_search_requests", "web_fetch_requests", "ephemeral_1h_input_tokens", "ephemeral_5m_input_tokens":
			if string(value) == "null" {
				continue
			}
		case "server_tool_use", "cache_creation":
			if string(value) == "null" {
				continue
			}
			var nestedPrevious, nestedIncoming Usage
			if json.Unmarshal(usage[key], &nestedPrevious) == nil || len(usage[key]) == 0 {
				if json.Unmarshal(value, &nestedIncoming) == nil && nestedIncoming != nil {
					value, _ = json.Marshal(mergeUsage(nestedPrevious, nestedIncoming))
				}
			}
		case "service_tier":
			if len(usage[key]) > 0 {
				continue // TS retains the initial request's service tier.
			}
		}
		usage[key] = append(json.RawMessage(nil), value...)
	}
	return usage
}
