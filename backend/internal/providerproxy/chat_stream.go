package providerproxy

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ChatStreamOptions struct {
	NowMillis func() int64
}
type chatStreamTool struct {
	id, name, arguments json.RawMessage
	started             bool
	index               int
}

// ChatProcessor is the original Chat SSE producer without transport ownership.
// A failed dispatch discards only its unflushed events and closes with an error
// frame; previously flushed blocks remain visible and never commit a turn.
type ChatProcessor struct {
	options                            ChatStreamOptions
	model                              json.RawMessage
	started, stopped, ended, cancelled bool
	blockType                          string
	blockIndex, next                   int
	blockStarted, blockStopped         bool
	tools                              map[string]*chatStreamTool
	toolOrder                          []string
	finishReason                       json.RawMessage
	held                               protocol.JSONObject
	queue                              [][]byte
	buffer, event                      string
	data                               []string
	utf8Pending                        []byte
	decoderStarted                     bool
}

func NewChatProcessor(model string, options ChatStreamOptions) *ChatProcessor {
	return &ChatProcessor{options: options, model: responseJSON(model), blockType: "text", blockIndex: -1, tools: map[string]*chatStreamTool{}}
}
func (p *ChatProcessor) emit(kind string, value protocol.JSONObject) {
	p.queue = append(p.queue, []byte("event: "+kind+"\ndata: "+string(responseJSON(value))+"\n\n"))
}
func (p *ChatProcessor) start(id json.RawMessage) {
	if p.started {
		return
	}
	p.started = true
	if !responseTruthy(id) {
		id = responseFallbackID(p.options.NowMillis)
	}
	message := responseRecord(responseRawField("id", id), responseField("type", "message"), responseField("role", "assistant"), responseField("content", []any{}), responseRawField("model", p.model), responseField("stop_reason", nil), responseField("stop_sequence", nil), responseField("usage", responseRecord(responseField("input_tokens", 0), responseField("output_tokens", 0))))
	p.emit("message_start", responseRecord(responseField("type", "message_start"), responseField("message", message)))
}
func (p *ChatProcessor) open(kind string, block protocol.JSONObject) {
	p.blockIndex = p.next
	p.next++
	p.blockType, p.blockStarted, p.blockStopped = kind, true, false
	p.emit("content_block_start", responseRecord(responseField("type", "content_block_start"), responseField("index", p.blockIndex), responseField("content_block", block)))
}
func (p *ChatProcessor) delta(index int, kind, key string, value json.RawMessage) {
	p.emit("content_block_delta", responseRecord(responseField("type", "content_block_delta"), responseField("index", index), responseField("delta", responseRecord(responseField("type", kind), responseRawField(key, value)))))
}
func (p *ChatProcessor) closeCurrent() {
	if !p.blockStarted || p.blockStopped {
		return
	}
	p.blockStopped = true
	p.emit("content_block_stop", responseRecord(responseField("type", "content_block_stop"), responseField("index", p.blockIndex)))
}
func (p *ChatProcessor) closeNonTool() {
	if p.blockType != "tool_use" {
		p.closeCurrent()
	}
}
func (p *ChatProcessor) closeAll() {
	p.closeNonTool()
	for _, key := range p.toolOrder {
		tool := p.tools[key]
		if tool.started {
			p.emit("content_block_stop", responseRecord(responseField("type", "content_block_stop"), responseField("index", tool.index)))
		}
	}
	p.tools, p.toolOrder = map[string]*chatStreamTool{}, nil
	if p.blockType == "tool_use" {
		p.blockStopped = true
	}
}
func chatReasoning(delta protocol.JSONObject) (json.RawMessage, json.RawMessage, error) {
	for _, key := range []string{"reasoning_content", "reasoning"} {
		if raw := delta.Get(key); responseString(raw) && responseTruthy(raw) {
			return raw, responseJSON(""), nil
		}
	}
	rows := requestArray(delta.Get("thinking_blocks"))
	if len(rows) == 0 {
		return nil, nil, nil
	}
	if err := mediaPropertyError(rows[0], "block.type"); err != nil {
		return nil, nil, err
	}
	block := responseObject(rows[0])
	if block.String("type") != "thinking" {
		return nil, nil, nil
	}
	thinking, signature := block.Get("thinking"), block.Get("signature")
	if !responseTruthy(thinking) {
		thinking = responseJSON("")
	}
	if !responseTruthy(signature) {
		signature = responseJSON("")
	}
	if !responseTruthy(thinking) && !responseTruthy(signature) {
		return nil, nil, nil
	}
	return thinking, signature, nil
}
func (p *ChatProcessor) process(chunk protocol.JSONObject) error {
	choices := chunk.Get("choices")
	choiceRaw := responseObject(choices).Get("0")
	if requestIsArray(choices) {
		rows := requestArray(choices)
		if len(rows) > 0 {
			choiceRaw = rows[0]
		}
	}
	if !responseTruthy(choiceRaw) {
		p.mergeUsage(chunk.Get("usage"))
		return nil
	}
	if responseTruthy(chunk.Get("model")) {
		p.model = chunk.Get("model")
	}
	p.start(chunk.Get("id"))
	choice := responseObject(choiceRaw)
	delta := responseObject(choice.Get("delta"))
	thinking, signature, err := chatReasoning(delta)
	if err != nil {
		return err
	}
	toolCalls := delta.Get("tool_calls")
	toolLength := responseObject(toolCalls).Get("length")
	if requestIsArray(toolCalls) {
		toolLength = responseJSON(len(requestArray(toolCalls)))
	}
	if requestIsString(toolCalls) {
		toolLength = responseJSON(len(responseUnits(toolCalls)))
	}
	if responseTruthy(p.finishReason) {
		p.mergeUsage(chunk.Get("usage"))
		if thinking != nil || responseTruthy(delta.Get("content")) || responseTruthy(toolLength) {
			return responseError("OpenAI Chat upstream sent content after finish_reason")
		}
		return nil
	}
	if thinking != nil {
		if p.blockType != "thinking" {
			p.closeNonTool()
		}
		if p.blockType != "thinking" || !p.blockStarted || p.blockStopped {
			p.open("thinking", responseRecord(responseField("type", "thinking"), responseField("thinking", "")))
		}
		if responseTruthy(thinking) {
			p.delta(p.blockIndex, "thinking_delta", "thinking", thinking)
		}
		if responseTruthy(signature) {
			p.delta(p.blockIndex, "signature_delta", "signature", signature)
		}
	}
	if content := delta.Get("content"); !responseNullish(content) && string(content) != `""` {
		if p.blockType != "text" {
			p.closeNonTool()
		}
		if p.blockType != "text" || !p.blockStarted || p.blockStopped {
			p.open("text", responseRecord(responseField("type", "text"), responseField("text", "")))
		}
		p.delta(p.blockIndex, "text_delta", "text", content)
	}
	if responseTruthy(toolLength) {
		p.closeNonTool()
		if err := p.toolCalls(toolCalls); err != nil {
			return err
		}
	}
	if responseTruthy(choice.Get("finish_reason")) {
		return p.finish(choice.Get("finish_reason"), chunk.Get("usage"))
	}
	return nil
}
func (p *ChatProcessor) toolCalls(raw json.RawMessage) error {
	if !requestIsArray(raw) {
		return responseError("delta.tool_calls is not iterable")
	}
	for _, row := range requestArray(raw) {
		if err := mediaPropertyError(row, "tc.index"); err != nil {
			return err
		}
		call := responseObject(row)
		var index float64
		if !responseNumeric(call.Get("index")) || json.Unmarshal(call.Get("index"), &index) != nil || index < 0 || math.IsInf(index, 0) || math.Trunc(index) != index {
			return responseError("OpenAI Chat tool delta is missing a valid index")
		}
		key := strconv.FormatFloat(index, 'g', -1, 64)
		tool := p.tools[key]
		if tool == nil {
			tool = &chatStreamTool{id: responseJSON(""), name: responseJSON(""), arguments: responseJSON(""), index: -1}
			p.tools[key] = tool
			p.toolOrder = append(p.toolOrder, key)
		}
		id := call.Get("id")
		if responseTruthy(id) {
			if responseTruthy(tool.id) && !bytesEqualResponse(tool.id, id) {
				return responseError("OpenAI Chat tool id changed within one index")
			}
			for _, other := range p.tools {
				if other != tool && bytesEqualResponse(other.id, id) {
					return responseError("OpenAI Chat tool id was reused for another index")
				}
			}
			tool.id = id
		}
		function := responseObject(call.Get("function"))
		if name := function.Get("name"); responseTruthy(name) {
			if tool.started && !bytesEqualResponse(tool.name, name) {
				return responseError("OpenAI Chat tool name changed after its block started")
			}
			if !tool.started && !bytesEqualResponse(tool.name, name) {
				tool.name = responseConcat(tool.name, name)
			}
		}
		arguments := stringifyResponsesToolArguments(function.Get("arguments"))
		if responseTruthy(arguments) {
			tool.arguments = responseConcat(tool.arguments, arguments)
		}
		if !tool.started && responseTruthy(tool.id) && responseTruthy(tool.name) {
			tool.started, tool.index = true, p.next
			p.next++
			p.blockType, p.blockIndex, p.blockStarted, p.blockStopped = "tool_use", tool.index, true, false
			p.emit("content_block_start", responseRecord(responseField("type", "content_block_start"), responseField("index", tool.index), responseField("content_block", responseRecord(responseField("type", "tool_use"), responseRawField("id", tool.id), responseRawField("name", tool.name), responseField("input", responseRecord())))))
			if responseTruthy(tool.arguments) {
				p.delta(tool.index, "input_json_delta", "partial_json", tool.arguments)
			}
		} else if tool.started && responseTruthy(arguments) {
			p.blockType, p.blockIndex, p.blockStopped = "tool_use", tool.index, false
			p.delta(tool.index, "input_json_delta", "partial_json", arguments)
		}
	}
	return nil
}
func bytesEqualResponse(a, b json.RawMessage) bool {
	return string(protocol.StringifyValidatedJavaScriptJSON(a)) == string(protocol.StringifyValidatedJavaScriptJSON(b))
}
func (p *ChatProcessor) finish(reason, usage json.RawMessage) error {
	var stop string
	switch string(reason) {
	case `"stop"`, `"content_filter"`:
		stop = "end_turn"
	case `"tool_calls"`:
		stop = "tool_use"
	case `"length"`:
		stop = "max_tokens"
	default:
		message, err := mediaInterpolation(responseJSON("OpenAI Chat upstream returned unknown finish_reason: "), reason)
		if err != nil {
			return err
		}
		return chatResponseFailure(message)
	}
	if string(reason) == `"content_filter"` && len(p.tools) > 0 {
		return responseError("OpenAI Chat upstream filtered a tool-call turn before it could be committed")
	}
	if string(reason) != `"length"` && string(reason) != `"content_filter"` {
		for _, key := range p.toolOrder {
			tool := p.tools[key]
			if !responseString(tool.id) {
				return responseError("block.id.trim is not a function")
			}
			if !responseTruthy(openAITrimJSON(tool.id)) {
				return responseError("OpenAI Chat tool call is missing its id or function name")
			}
			if !responseString(tool.name) {
				return responseError("block.name.trim is not a function")
			}
			if !responseTruthy(openAITrimJSON(tool.name)) {
				return responseError("OpenAI Chat tool call is missing its id or function name")
			}
			if _, err := ParseCompleteChatToolArguments(tool.arguments); err != nil {
				return err
			}
		}
		if stop == "tool_use" && len(p.tools) == 0 {
			return responseError("OpenAI Chat upstream finished with tool_calls but supplied no tool calls")
		}
	}
	p.finishReason = reason
	p.closeAll()
	mapped := responseRecord(responseField("output_tokens", 0))
	if responseTruthy(usage) {
		mapped = OpenAIUsageToAnthropic(usage)
	}
	p.held = responseRecord(responseField("type", "message_delta"), responseField("delta", responseRecord(responseField("stop_reason", stop), responseField("stop_sequence", nil))), responseField("usage", mapped))
	return nil
}
func (p *ChatProcessor) mergeUsage(usage json.RawMessage) {
	if responseTruthy(usage) && p.held.IsObject() {
		p.held.Set("usage", responseJSON(OpenAIUsageToAnthropic(usage)))
	}
}
func (p *ChatProcessor) finalize() {
	if p.stopped {
		return
	}
	p.stopped = true
	if !responseTruthy(p.finishReason) {
		p.emitError("stream_truncated", responseJSON("OpenAI Chat upstream stream ended without finish_reason"))
		return
	}
	p.start(nil)
	p.closeAll()
	if p.held.IsObject() {
		p.emit("message_delta", p.held)
		p.held = protocol.JSONObject{}
	}
	p.emit("message_stop", responseRecord(responseField("type", "message_stop")))
}
func (p *ChatProcessor) emitError(kind string, message json.RawMessage) {
	p.emit("error", responseRecord(responseField("type", "error"), responseField("error", responseRecord(responseField("type", kind), responseRawField("message", message)))))
}
func (p *ChatProcessor) dispatch() error {
	if p.ended || len(p.data) == 0 {
		p.event = ""
		return nil
	}
	data := strings.Join(p.data, "\n")
	p.data = nil
	if protocol.TrimJavaScriptString(data) == "[DONE]" {
		p.finalize()
		p.ended = true
		return nil
	}
	parsed, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(data))
	if err != nil {
		return responseError("OpenAI Chat upstream sent malformed SSE JSON")
	}
	policy, policyFound := GetOpenAIPolicyError(parsed)
	upstreamErr := ChatResponseError(parsed)
	if policyFound || upstreamErr != nil || p.event == "error" {
		if policyFound {
			errorBody := responseRecord(responseField("type", "permission_error"))
			errorBody.Assign(policy)
			p.emit("error", responseRecord(responseField("type", "error"), responseField("error", errorBody)))
		} else {
			message := responseJSON("OpenAI Chat upstream reported an error")
			if upstreamErr != nil {
				message = responseJSON(upstreamErr.Error())
				if failure, ok := upstreamErr.(*ResponseError); ok && failure.MessageJSON != nil {
					message = failure.MessageJSON
				}
			}
			p.emitError("api_error", message)
		}
		p.ended = true
		return nil
	}
	p.event = ""
	return p.process(responseObject(parsed))
}
func (p *ChatProcessor) consumeLine(line string) error {
	if line == "" {
		return p.dispatch()
	}
	if strings.HasPrefix(line, ":") {
		return nil
	}
	field, value, found := strings.Cut(line, ":")
	if !found {
		value = ""
	}
	value = strings.TrimPrefix(value, " ")
	if field == "data" {
		p.data = append(p.data, value)
	}
	if field == "event" {
		p.event = value
	}
	return nil
}
func (p *ChatProcessor) decode(chunk []byte, final bool) string {
	data := append(append([]byte(nil), p.utf8Pending...), chunk...)
	size := len(data)
	if !final {
		size = responseUTF8Prefix(data)
	}
	p.utf8Pending = append([]byte(nil), data[size:]...)
	decoded := protocol.DecodeJavaScriptUTF8(data[:size])
	// Bun's Source decoder consumes its BOM decision on the first nonempty
	// byte chunk, even when that chunk contains only a partial BOM.
	if !p.decoderStarted && len(data) > 0 {
		p.decoderStarted = true
		if len(decoded) >= 3 && decoded[0] == 0xef && decoded[1] == 0xbb && decoded[2] == 0xbf {
			decoded = decoded[3:]
		}
	}
	return string(decoded)
}
func (p *ChatProcessor) consume(final bool) ([][]byte, error) {
	flushed := [][]byte{}
	flush := func() { flushed = append(flushed, p.queue...); p.queue = nil }
	for !p.ended {
		n := strings.IndexAny(p.buffer, "\r\n")
		if n < 0 {
			break
		}
		if !final && p.buffer[n] == '\r' && n == len(p.buffer)-1 {
			break
		}
		width := 1
		if p.buffer[n] == '\r' && n+1 < len(p.buffer) && p.buffer[n+1] == '\n' {
			width = 2
		}
		line := p.buffer[:n]
		p.buffer = p.buffer[n+width:]
		if err := p.consumeLine(line); err != nil {
			return flushed, err
		}
		if line == "" {
			flush()
		}
	}
	if final && !p.ended {
		if p.buffer != "" {
			if err := p.consumeLine(p.buffer); err != nil {
				return flushed, err
			}
		}
		p.buffer = ""
		if err := p.dispatch(); err != nil {
			return flushed, err
		}
	}
	flush()
	return flushed, nil
}
func (p *ChatProcessor) failure(err error) [][]byte {
	p.queue = nil
	p.ended = true
	message := responseJSON(err.Error())
	if failure, ok := err.(*ResponseError); ok && failure.MessageJSON != nil {
		message = failure.MessageJSON
	}
	p.emitError("stream_error", message)
	frames := p.queue
	p.queue = nil
	return frames
}
func (p *ChatProcessor) Feed(chunk []byte) ([][]byte, bool) {
	if p.ended || p.cancelled {
		return nil, true
	}
	p.buffer += p.decode(chunk, false)
	frames, err := p.consume(false)
	if err != nil {
		frames = append(frames, p.failure(err)...)
	}
	return frames, p.ended
}
func (p *ChatProcessor) Finish() [][]byte {
	if p.ended || p.cancelled {
		return nil
	}
	p.buffer += p.decode(nil, true)
	frames, err := p.consume(true)
	if err != nil {
		return append(frames, p.failure(err)...)
	}
	if !p.ended {
		p.finalize()
	}
	frames = append(frames, p.queue...)
	p.queue = nil
	p.ended = true
	return frames
}
func (p *ChatProcessor) Fail(err error) [][]byte {
	if p.ended || p.cancelled {
		return nil
	}
	return p.failure(err)
}
func (p *ChatProcessor) Cancel() { p.cancelled = true; p.queue = nil }
