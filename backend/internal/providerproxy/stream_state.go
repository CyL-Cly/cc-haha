package providerproxy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ResponsesStreamOptions struct {
	OpenAICodexOAuth bool
	NowMillis        func() int64
	OnTerminal       func(string)
	OnCancel         func(any)
	OnSettled        func()
}
type responseTool struct{ id, name, arguments json.RawMessage }

// ResponsesProcessor is an incremental pure protocol producer. Feed/Finish
// must run on its owning caller. It neither reads transport nor starts models;
// the native provider can retain its existing stream/Owner/trace ownership.
type ResponsesProcessor struct {
	options                                        ResponsesStreamOptions
	model, messageID                               json.RawMessage
	started, stopped, terminal, cancelled, settled bool
	next                                           int
	indexByKey, mapReasoning, mapItem, mapOutput   map[string]int
	reasoningOrder                                 []string
	reasoningDone                                  map[string]bool
	tools                                          map[int]*responseTool
	toolOrder                                      []int
	texts                                          map[int]json.RawMessage
	open                                           map[int]bool
	frames                                         [][]byte
	lastEvent                                      json.RawMessage
	buffer, currentEvent                           string
	dataLines                                      []string
	utf8Pending                                    []byte
	decoderStarted                                 bool
}

func NewResponsesProcessor(model string, o ResponsesStreamOptions) *ResponsesProcessor {
	return &ResponsesProcessor{options: o, model: responseJSON(model), messageID: responseFallbackID(o.NowMillis), indexByKey: map[string]int{}, mapReasoning: map[string]int{}, mapItem: map[string]int{}, mapOutput: map[string]int{}, reasoningDone: map[string]bool{}, tools: map[int]*responseTool{}, texts: map[int]json.RawMessage{}, open: map[int]bool{}}
}
func (p *ResponsesProcessor) TerminalSeen() bool { return p.terminal }
func (p *ResponsesProcessor) Cancel(reason any) {
	if p.cancelled {
		return
	}
	p.cancelled = true
	if !p.terminal && p.options.OnCancel != nil {
		p.options.OnCancel(reason)
	}
}
func (p *ResponsesProcessor) Settled() {
	if p.settled {
		return
	}
	p.settled = true
	if p.options.OnSettled != nil {
		p.options.OnSettled()
	}
}
func (p *ResponsesProcessor) emit(kind string, value protocol.JSONObject) {
	p.frames = append(p.frames, []byte("event: "+kind+"\ndata: "+string(responseJSON(value))+"\n\n"))
}
func (p *ResponsesProcessor) messageStart() {
	if p.started {
		return
	}
	p.started = true
	message := responseRecord(responseRawField("id", p.messageID), responseField("type", "message"), responseField("role", "assistant"), responseField("content", []any{}), responseRawField("model", p.model), responseField("stop_reason", nil), responseField("stop_sequence", nil), responseField("usage", responseRecord(responseField("input_tokens", 0), responseField("output_tokens", 0))))
	p.emit("message_start", responseRecord(responseField("type", "message_start"), responseField("message", message)))
}
func (p *ResponsesProcessor) messageStop() {
	if p.stopped {
		return
	}
	p.messageStart()
	p.stopped = true
	p.emit("message_stop", responseRecord(responseField("type", "message_stop")))
}
func (p *ResponsesProcessor) startBlock(index int, block protocol.JSONObject) {
	p.emit("content_block_start", responseRecord(responseField("type", "content_block_start"), responseField("index", index), responseField("content_block", block)))
}
func (p *ResponsesProcessor) delta(index int, kind, key string, value json.RawMessage) {
	p.emit("content_block_delta", responseRecord(responseField("type", "content_block_delta"), responseField("index", index), responseField("delta", responseRecord(responseField("type", kind), responseRawField(key, value)))))
}
func (p *ResponsesProcessor) stopBlock(index int) {
	p.emit("content_block_stop", responseRecord(responseField("type", "content_block_stop"), responseField("index", index)))
}
func (p *ResponsesProcessor) closeBlock(index int) {
	if !p.open[index] {
		return
	}
	delete(p.open, index)
	p.stopBlock(index)
}
func responseKey(value json.RawMessage) string {
	if responseNullish(value) {
		value = responseJSON(0)
	}
	if responseString(value) {
		return "s:" + string(protocol.QuoteJavaScriptUTF16(responseUnits(value)))
	}
	if responseArray(value) || len(value) > 0 && value[0] == '{' {
		return "object:" + string(value)
	}
	if string(value) == "true" || string(value) == "false" {
		return "b:" + string(value)
	}
	normal, _ := protocol.ParseAndStringifyJavaScriptJSON(value)
	return "n:" + string(normal)
}
func responseNumeric(value json.RawMessage) bool {
	return len(value) > 0 && (value[0] == '-' || value[0] >= '0' && value[0] <= '9')
}
func responseStringKey(value json.RawMessage) string {
	return string(protocol.QuoteJavaScriptUTF16(responseUnits(value)))
}
func (p *ResponsesProcessor) ensureText(data protocol.JSONObject) int {
	a, b := data.Get("output_index"), data.Get("content_index")
	if responseNullish(a) {
		a = responseJSON(0)
	}
	if responseNullish(b) {
		b = responseJSON(0)
	}
	keyRaw := responseConcat(a, responseJSON(":"), b)
	key := responseStringKey(keyRaw)
	if index, ok := p.indexByKey[key]; ok {
		return index
	}
	p.messageStart()
	index := p.next
	p.next++
	p.indexByKey[key] = index
	p.texts[index] = responseJSON("")
	p.open[index] = true
	p.startBlock(index, responseRecord(responseField("type", "text"), responseField("text", "")))
	return index
}
func (p *ResponsesProcessor) reconcileText(index int, text json.RawMessage) error {
	previous := responseUnits(p.texts[index])
	units := responseUnits(text)
	if equalResponseUnits(units, previous) {
		return nil
	}
	if !prefixResponseUnits(units, previous) || !p.open[index] {
		return responseError("OpenAI Responses text snapshot conflicts with streamed content")
	}
	p.texts[index] = append(json.RawMessage(nil), text...)
	p.delta(index, "text_delta", "text", protocol.QuoteJavaScriptUTF16(units[len(previous):]))
	return nil
}
func equalResponseUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func prefixResponseUnits(a, b []uint16) bool {
	return len(a) >= len(b) && equalResponseUnits(a[:len(b)], b)
}
func (p *ResponsesProcessor) ensureReasoning(data protocol.JSONObject) int {
	key := responseKey(data.Get("output_index"))
	if index, ok := p.mapReasoning[key]; ok {
		return index
	}
	p.messageStart()
	index := p.next
	p.next++
	p.mapReasoning[key] = index
	p.reasoningOrder = append(p.reasoningOrder, key)
	p.startBlock(index, responseRecord(responseField("type", "thinking"), responseField("thinking", "")))
	return index
}
func (p *ResponsesProcessor) closeReasoning(data protocol.JSONObject) {
	value := data.Get("output_index")
	if !responseNumeric(value) {
		value = responseJSON(0)
	}
	key := responseKey(value)
	index, ok := p.mapReasoning[key]
	if !ok {
		return
	}
	item := responseObject(data.Get("item"))
	signature := item.Get("encrypted_content")
	if responseString(signature) && responseTruthy(signature) {
		p.delta(index, "signature_delta", "signature", signature)
	}
	p.stopBlock(index)
	delete(p.mapReasoning, key)
	for i, existing := range p.reasoningOrder {
		if existing == key {
			p.reasoningOrder = append(p.reasoningOrder[:i], p.reasoningOrder[i+1:]...)
			break
		}
	}
}
func (p *ResponsesProcessor) ensureTool(data, item protocol.JSONObject) (int, error) {
	id := item.Get("id")
	if !responseString(id) {
		id = responseJSON("")
	}
	callID := item.Get("call_id")
	if !responseString(callID) {
		callID = id
	}
	name := item.Get("name")
	if !responseString(name) {
		name = responseJSON("")
	}
	keyID := id
	if !responseTruthy(keyID) {
		keyID = callID
	}
	key := responseStringKey(keyID)
	index, exists := p.mapItem[key]
	output := data.Get("output_index")
	if !exists && responseNumeric(output) {
		index, exists = p.mapOutput[responseKey(output)]
	}
	if exists {
		tool := p.tools[index]
		if responseTruthy(callID) && !equalResponseUnits(responseUnits(callID), responseUnits(tool.id)) || responseTruthy(name) && !equalResponseUnits(responseUnits(name), responseUnits(tool.name)) {
			return 0, responseError("OpenAI Responses tool snapshot conflicts with tool identity")
		}
		if responseTruthy(id) {
			p.mapItem[responseStringKey(id)] = index
		}
		return index, nil
	}
	if !responseTruthy(callID) || !responseTruthy(name) {
		return 0, responseError("Invalid OpenAI Responses tool identity")
	}
	p.messageStart()
	index = p.next
	p.next++
	p.mapItem[key] = index
	if responseNumeric(output) {
		p.mapOutput[responseKey(output)] = index
	}
	p.tools[index] = &responseTool{id: idOrCall(callID), name: name, arguments: responseJSON("")}
	p.toolOrder = append(p.toolOrder, index)
	p.open[index] = true
	p.startBlock(index, responseRecord(responseField("type", "tool_use"), responseRawField("id", callID), responseRawField("name", name), responseField("input", responseRecord())))
	return index, nil
}
func idOrCall(id json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), id...) }
func (p *ResponsesProcessor) reconcileTool(index int, value json.RawMessage) error {
	text := stringifyResponsesToolArguments(value)
	tool := p.tools[index]
	units, previous := responseUnits(text), responseUnits(tool.arguments)
	if equalResponseUnits(units, previous) {
		return nil
	}
	if !prefixResponseUnits(units, previous) {
		if responseArgumentsEqual(units, previous) {
			return nil
		}
		return responseError("OpenAI Responses tool arguments snapshot conflicts with streamed arguments")
	}
	tool.arguments = text
	if len(units) > len(previous) {
		p.delta(index, "input_json_delta", "partial_json", protocol.QuoteJavaScriptUTF16(units[len(previous):]))
	}
	return nil
}
func (p *ResponsesProcessor) toolIndex(data protocol.JSONObject, allowOutput bool) (int, bool) {
	itemID := data.Get("item_id")
	if !responseTruthy(itemID) {
		itemID = responseJSON("")
	}
	if responseString(itemID) {
		if index, ok := p.mapItem[responseStringKey(itemID)]; ok {
			return index, true
		}
	}
	if allowOutput {
		index, ok := p.mapOutput[responseKey(data.Get("output_index"))]
		return index, ok
	}
	return 0, false
}
func (p *ResponsesProcessor) terminalError(err error) {
	failure, ok := err.(*ResponseError)
	if !ok {
		failure = responseError(err.Error())
	}
	kind := failure.Type
	if kind == "" {
		kind = "api_error"
	}
	body := responseRecord(responseField("type", kind), responseRawField("message", failure.MessageJSON))
	if failure.Code != "" {
		body.Set("code", responseJSON(failure.Code))
	}
	p.emit("error", responseRecord(responseField("type", "error"), responseField("error", body)))
}
func (p *ResponsesProcessor) terminalOutput(data protocol.JSONObject, event string) (bool, error) {
	p.terminal = true
	response := responseObject(data.Get("response"))
	if !response.IsObject() {
		response = data
	}
	if responseString(response.Get("model")) {
		p.model = response.Get("model")
	}
	if responseString(response.Get("id")) {
		p.messageID = response.Get("id")
	}
	terminal, err := responsesTerminal(response, event)
	if err != nil {
		p.terminalError(err)
		return true, nil
	}
	output := response.Get("output")
	if terminal == "completed" && output == nil && p.next == 0 {
		return false, responseError("Invalid OpenAI Responses completed response: missing output")
	}
	if output != nil && !responseArray(output) {
		return false, responseError("Invalid OpenAI Responses output: expected an array")
	}
	if responseArray(output) {
		var rows []json.RawMessage
		_ = json.Unmarshal(output, &rows)
		for index, item := range rows {
			if terminal == "max_tokens" && responseObject(item).String("type") == "function_call" {
				continue
			}
			_, err := p.processEvent("response.output_item.done", responseRecord(responseField("output_index", index), responseRawField("item", item)))
			if err != nil {
				return false, err
			}
		}
	}
	if terminal == "completed" {
		for _, index := range p.toolOrder {
			tool := p.tools[index]
			if !responseTruthy(tool.id) || !responseTruthy(tool.name) {
				return false, responseError("Invalid OpenAI Responses tool identity")
			}
			if _, err := ParseResponsesToolArguments(tool.arguments); err != nil {
				return false, err
			}
		}
	}
	p.messageStart()
	for _, key := range p.reasoningOrder {
		if index, ok := p.mapReasoning[key]; ok {
			p.stopBlock(index)
			delete(p.mapReasoning, key)
		}
	}
	indices := []int{}
	for index := range p.open {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		p.closeBlock(index)
	}
	stop := "end_turn"
	if terminal == "max_tokens" {
		stop = "max_tokens"
	} else if len(p.tools) > 0 {
		stop = "tool_use"
	}
	p.emit("message_delta", responseRecord(responseField("type", "message_delta"), responseField("delta", responseRecord(responseField("stop_reason", stop), responseField("stop_sequence", nil))), responseField("usage", OpenAIUsageToAnthropic(response.Get("usage")))))
	p.messageStop()
	return true, nil
}

func (p *ResponsesProcessor) takeFrames() [][]byte { frames := p.frames; p.frames = nil; return frames }
func (p *ResponsesProcessor) premature() error {
	last := p.lastEvent
	if last == nil {
		last = responseJSON("none")
	}
	message := responseConcat(responseJSON("OpenAI Responses stream closed before response.completed (last event: "), last, responseJSON(")"))
	return &ResponseError{Message: responseText(message), MessageJSON: message, Code: "ERR_STREAM_PREMATURE_CLOSE"}
}
func responsePartKind(value json.RawMessage) bool {
	text, _ := protocol.JavaScriptStringJSON(value)
	switch responseText(text) {
	case "output_text", "text", "refusal":
		return true
	}
	return false
}
func (p *ResponsesProcessor) resetEvent() { p.currentEvent = ""; p.dataLines = nil }
func (p *ResponsesProcessor) dispatchEvent() (bool, error) {
	if len(p.dataLines) == 0 {
		p.resetEvent()
		return false, nil
	}
	text, event := strings.Join(p.dataLines, "\n"), p.currentEvent
	p.resetEvent()
	if text == "[DONE]" {
		p.lastEvent = responseJSON("[DONE]")
		return true, nil
	}
	var data protocol.JSONObject
	if err := data.UnmarshalJSON([]byte(text)); err != nil || !data.IsObject() {
		return false, responseError("Invalid OpenAI Responses SSE JSON")
	}
	if event == "" && responseString(data.Get("type")) {
		event = data.String("type")
	}
	if event == "" {
		return false, nil
	}
	p.lastEvent = responseJSON(event)
	terminal, err := p.processEvent(event, data)
	if err == nil && terminal && p.options.OnTerminal != nil {
		p.options.OnTerminal(event)
	}
	return terminal, err
}
func (p *ResponsesProcessor) line(raw string) (bool, error) {
	line := strings.TrimSuffix(raw, "\r")
	if line == "" {
		return p.dispatchEvent()
	}
	if strings.HasPrefix(line, ":") {
		return false, nil
	}
	field, value, found := strings.Cut(line, ":")
	if !found {
		value = ""
	}
	value = strings.TrimPrefix(value, " ")
	switch field {
	case "event":
		p.currentEvent = value
	case "data":
		p.dataLines = append(p.dataLines, value)
	}
	return false, nil
}
func (p *ResponsesProcessor) Feed(chunk []byte) ([][]byte, bool, error) {
	if p.cancelled {
		return nil, false, nil
	}
	if p.terminal {
		return nil, true, nil
	}
	p.buffer += p.decode(chunk, false)
	terminal := false
	for {
		newline := strings.IndexByte(p.buffer, '\n')
		if newline < 0 {
			break
		}
		line := p.buffer[:newline]
		p.buffer = p.buffer[newline+1:]
		var err error
		terminal, err = p.line(line)
		if err != nil {
			return p.takeFrames(), p.terminal, err
		}
		if terminal {
			break
		}
	}
	return p.takeFrames(), terminal, nil
}
func (p *ResponsesProcessor) Finish() ([][]byte, error) {
	if p.cancelled {
		return p.takeFrames(), nil
	}
	if !p.terminal {
		p.buffer += p.decode(nil, true)
		if p.buffer != "" {
			_, err := p.line(p.buffer)
			if err != nil {
				return p.takeFrames(), err
			}
			p.buffer = ""
		}
		if _, err := p.dispatchEvent(); err != nil {
			return p.takeFrames(), err
		}
	}
	if !p.terminal {
		return p.takeFrames(), p.premature()
	}
	return p.takeFrames(), nil
}

func (p *ResponsesProcessor) String() string {
	return fmt.Sprintf("ResponsesProcessor(started=%t terminal=%t blocks=%d)", p.started, p.terminal, p.next)
}
