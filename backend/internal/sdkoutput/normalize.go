// Package sdkoutput ports the output normalization component. It is not an
// ASK host, model consumer, transcript writer, result or accounting service.
package sdkoutput

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "SDK output capability is not migrated: " + e.Boundary
}
func cap(boundary string) error { return &CapabilityError{boundary} }

type Dependencies struct {
	Factory     *messages.Factory
	SessionID   func() json.RawMessage
	NowMillis   func() float64
	OriginalEnv func(string) string
}
type Normalizer struct {
	deps          Dependencies
	progress      map[string]float64
	progressOrder []string
}

func New(deps Dependencies) (*Normalizer, error) {
	if deps.Factory == nil || deps.SessionID == nil || deps.NowMillis == nil || deps.OriginalEnv == nil {
		return nil, errors.New("SDK output requires the actual shared Factory, session, clock and original env")
	}
	return &Normalizer{deps: deps, progress: map[string]float64{}}, nil
}

// Message owns raw original fields and block objects. For a live provider
// envelope it borrows that exact envelope; no Source output is accepted.
// All normalization, mutation and serialization uses one caller event owner.
type Message struct {
	Record  protocol.JSONObject
	API     protocol.JSONObject
	Content []*streamhistory.Block
	Live    *streamhistory.Envelope
	// Nested borrows an actual live progress producer's message. Raw JSON-only
	// progress remains decodable, but cannot express later object mutations.
	Nested *Message
}

func Decode(raw []byte) (Message, error) {
	var record protocol.JSONObject
	if err := record.UnmarshalJSON(raw); err != nil || !record.IsObject() {
		return Message{}, cap("original message object")
	}
	message := Message{Record: record}
	kind := record.String("type")
	if kind != "assistant" && kind != "user" {
		return message, nil
	}
	if err := message.API.UnmarshalJSON(record.Get("message")); err != nil || !message.API.IsObject() {
		return Message{}, cap("original API message object")
	}
	content := bytes.TrimSpace(message.API.Get("content"))
	if len(content) == 0 {
		return Message{}, cap("missing original content")
	}
	if content[0] == '"' {
		if kind == "assistant" {
			return Message{}, cap("assistant array content")
		}
		return message, nil
	}
	var blocks []json.RawMessage
	if len(content) == 0 || content[0] != '[' || json.Unmarshal(content, &blocks) != nil {
		return Message{}, cap("original content array/string")
	}
	for _, raw := range blocks {
		var block protocol.JSONObject
		if err := block.UnmarshalJSON(raw); err != nil || !block.IsObject() {
			return Message{}, cap("original content block object")
		}
		message.Content = append(message.Content, &streamhistory.Block{Object: block})
	}
	return message, nil
}
func FromEnvelope(value *streamhistory.Envelope) (Message, error) {
	if value == nil || !value.Record.IsObject() || value.Record.String("type") != "assistant" || !value.Message.IsObject() || value.Content == nil {
		return Message{}, cap("actual live assistant envelope")
	}
	return Message{Record: value.Record, API: value.Message, Content: value.Content, Live: value}, nil
}
func (m Message) MarshalJSON() ([]byte, error) {
	copy := m.Record.Clone()
	if m.API.IsObject() {
		api := m.API.Clone()
		if m.Live != nil && m.Live.Usage != nil {
			api.Set("usage", m.Live.Usage.Raw)
		}
		if !isString(api.Get("content")) || m.Live != nil {
			api.Set("content", blockArray(m.Content))
		}
		raw, err := api.MarshalJSON()
		if err != nil {
			return nil, err
		}
		copy.Set("message", raw)
	}
	return stringify(copy)
}
func blockArray(blocks []*streamhistory.Block) json.RawMessage {
	values := make([]protocol.JSONObject, 0, len(blocks))
	for _, block := range blocks {
		values = append(values, block.Object)
	}
	raw, _ := json.Marshal(values)
	return raw
}
func stringify(object protocol.JSONObject) ([]byte, error) {
	raw, err := object.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return protocol.ParseAndStringifyJavaScriptJSON(raw)
}
func isString(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '"'
}
func truthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if raw[0] == '"' {
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		return len(units) != 0
	}
	if raw[0] == '{' || raw[0] == '[' || raw[0] == 't' {
		return true
	}
	n, _ := strconv.ParseFloat(string(raw), 64)
	return n != 0 && !math.IsNaN(n)
}
func quote(s string) json.RawMessage { v, _ := json.Marshal(s); return v }
func nullish(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
func (m Message) captureAPI() protocol.JSONObject {
	api := m.API.Clone()
	if m.Live != nil && m.Live.Usage != nil {
		api.Set("usage", m.Live.Usage.Raw)
	}
	return api
}

// View snapshots the normalized outer/API fields at the Source spread point,
// while retaining the actual selected content block object. It does not claim
// arbitrary nested JS object reference identity for raw JSON fields.
type View struct {
	Record protocol.JSONObject
	api    protocol.JSONObject
	block  *streamhistory.Block
}

func (view *View) MarshalJSON() ([]byte, error) {
	if view == nil {
		return []byte("null"), nil
	}
	record := view.Record.Clone()
	if view.api.IsObject() {
		api := view.api.Clone()
		if view.block != nil {
			api.Set("content", blockArray([]*streamhistory.Block{view.block}))
		}
		raw, err := api.MarshalJSON()
		if err != nil {
			return nil, err
		}
		record.Set("message", raw)
	}
	return stringify(record)
}
func DerivedUUID(parent json.RawMessage, index int) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(parent)
	if err != nil || index < 0 {
		return nil, cap("string parent UUID and nonnegative block index")
	}
	if len(units) > 24 {
		units = units[:24]
	}
	hex := strconv.FormatInt(int64(index), 16)
	if len(hex) < 12 {
		hex = strings.Repeat("0", 12-len(hex)) + hex
	}
	for _, c := range hex {
		units = append(units, uint16(c))
	}
	return protocol.QuoteJavaScriptUTF16(units), nil
}
func (n *Normalizer) NormalizeMessages(input []Message) ([]*View, error) {
	out := []*View{}
	newChain := false
	for _, message := range input {
		kind := message.Record.String("type")
		switch kind {
		case "attachment", "progress", "system":
			out = append(out, &View{Record: message.Record})
		case "assistant":
			if !message.API.IsObject() || isString(message.API.Get("content")) {
				return nil, cap("original assistant content blocks")
			}
			newChain = newChain || len(message.Content) > 1
			for index, block := range message.Content {
				uuid := message.Record.Get("uuid")
				if newChain {
					var err error
					uuid, err = DerivedUUID(uuid, index)
					if err != nil {
						return nil, err
					}
				}
				api := message.captureAPI()
				api.Set("content", blockArray([]*streamhistory.Block{block}))
				context := api.Get("context_management")
				if nullish(context) {
					context = json.RawMessage("null")
				}
				api.Set("context_management", context)
				record := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("assistant")}, protocol.JSONField{Name: "timestamp", Value: message.Record.Get("timestamp")})
				record.Set("message", nil)
				for _, name := range []string{"isMeta", "isVirtual", "requestId"} {
					record.Set(name, message.Record.Get(name))
				}
				record.Set("uuid", uuid)
				for _, name := range []string{"error", "isApiErrorMessage", "apiError", "errorDetails", "businessErrorCode", "advisorModel"} {
					record.Set(name, message.Record.Get(name))
				}
				out = append(out, &View{Record: record, api: api, block: block})
			}
		case "user":
			if !message.API.IsObject() {
				return nil, cap("original user API")
			}
			if isString(message.API.Get("content")) {
				record := message.Record.Clone()
				uuid := record.Get("uuid")
				if newChain {
					var err error
					uuid, err = DerivedUUID(uuid, 0)
					if err != nil {
						return nil, err
					}
				}
				record.Set("uuid", uuid)
				api := message.captureAPI()
				block := &streamhistory.Block{Object: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("text")}, protocol.JSONField{Name: "text", Value: api.Get("content")})}
				api.Set("content", blockArray([]*streamhistory.Block{block}))
				out = append(out, &View{Record: record, api: api, block: block})
				continue
			}
			newChain = newChain || len(message.Content) > 1
			imageIndex := 0
			var imageIDs []json.RawMessage
			if raw := message.Record.Get("imagePasteIds"); truthy(raw) {
				if json.Unmarshal(raw, &imageIDs) != nil {
					return nil, cap("array imagePasteIds")
				}
			}
			for index, block := range message.Content {
				options := storage.Object{"content": blockArray([]*streamhistory.Block{block})}
				for _, name := range []string{"toolUseResult", "mcpMeta", "isMeta", "isVisibleInTranscriptOnly", "isVirtual", "timestamp", "origin"} {
					options[name] = message.Record.Get(name)
				}
				if block.Object.String("type") == "image" {
					if imageIndex < len(imageIDs) {
						raw, _ := json.Marshal([]json.RawMessage{imageIDs[imageIndex]})
						options["imagePasteIds"] = raw
					}
					imageIndex++
				}
				created := messages.OrderedUser(n.deps.Factory.CreateUser(options))
				uuid := message.Record.Get("uuid")
				if newChain {
					var err error
					uuid, err = DerivedUUID(uuid, index)
					if err != nil {
						return nil, err
					}
				}
				created.Set("uuid", uuid)
				var api protocol.JSONObject
				_ = api.UnmarshalJSON(created.Get("message"))
				out = append(out, &View{Record: created, api: api, block: block})
			}
		default:
			return nil, cap("normalizeMessages input type " + kind)
		}
	}
	return out, nil
}
func jsSpace(u uint16) bool {
	return u == 9 || u == 10 || u == 11 || u == 12 || u == 13 || u == 32 || u == 0xa0 || u == 0x1680 || (u >= 0x2000 && u <= 0x200a) || u == 0x2028 || u == 0x2029 || u == 0x202f || u == 0x205f || u == 0x3000 || u == 0xfeff
}
func nonempty(view *View) (bool, error) {
	if view.block == nil {
		return false, nil
	}
	if view.block.Object.String("type") != "text" {
		return true, nil
	}
	text := view.block.Object.Get("text")
	units, err := protocol.DecodeJavaScriptJSONString(text)
	if err != nil {
		return false, cap("text block string trim")
	}
	original := append([]uint16(nil), units...)
	for len(units) > 0 && jsSpace(units[0]) {
		units = units[1:]
	}
	for len(units) > 0 && jsSpace(units[len(units)-1]) {
		units = units[:len(units)-1]
	}
	if len(units) == 0 {
		return false, nil
	}
	return !sameUTF16(original, messages.NoContent) && !sameUTF16(original, messages.InterruptForToolUse), nil
}
func spread(raw json.RawMessage) (protocol.JSONObject, error) {
	raw = bytes.TrimSpace(raw)
	object := protocol.NewJSONObject()
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return object, nil
	}
	switch raw[0] {
	case '{':
		if err := object.UnmarshalJSON(raw); err != nil {
			return object, err
		}
	case '[':
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return object, err
		}
		for index, value := range entries {
			object.Set(strconv.Itoa(index), value)
		}
	case '"':
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return object, err
		}
		for index, unit := range units {
			object.Set(strconv.Itoa(index), protocol.QuoteJavaScriptUTF16([]uint16{unit}))
		}
	}
	return object, nil
}
func (n *Normalizer) sdkView(view *View, parent json.RawMessage) (*View, error) {
	kind := view.Record.String("type")
	record := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote(kind)})
	record.Set("message", nil)
	record.Set("parent_tool_use_id", parent)
	record.Set("session_id", n.deps.SessionID())
	record.Set("uuid", view.Record.Get("uuid"))
	if kind == "assistant" {
		record.Set("error", view.Record.Get("error"))
	} else {
		record.Set("timestamp", view.Record.Get("timestamp"))
		synthetic := view.Record.Get("isMeta")
		if !truthy(synthetic) {
			synthetic = view.Record.Get("isVisibleInTranscriptOnly")
		}
		record.Set("isSynthetic", synthetic)
		tool := view.Record.Get("toolUseResult")
		if truthy(view.Record.Get("mcpMeta")) {
			meta, err := spread(view.Record.Get("mcpMeta"))
			if err != nil {
				return nil, err
			}
			merged := protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: tool})
			merged.Assign(meta)
			tool, _ = merged.MarshalJSON()
		}
		record.Set("tool_use_result", tool)
	}
	return &View{Record: record, api: view.api, block: view.block}, nil
}
func envTruthy(value string) bool {
	units, _ := protocol.DecodeJavaScriptJSONString(quote(value))
	for len(units) > 0 && jsSpace(units[0]) {
		units = units[1:]
	}
	for len(units) > 0 && jsSpace(units[len(units)-1]) {
		units = units[:len(units)-1]
	}
	lower := strings.ToLower(string(protocol.QuoteJavaScriptUTF16(units)))
	return lower == `"1"` || lower == `"true"` || lower == `"yes"` || lower == `"on"`
}

// NormalizeMessage emits inline like the actual generator: normalization of
// block arrays is eager, while session/parent getters execute at each yield.
// Returning an error stops that exact emitter. It never consumes model data.
func (n *Normalizer) NormalizeMessage(message Message, emit func(*View) error) error {
	if emit == nil {
		return errors.New("SDK output requires an original consumer")
	}
	kind := message.Record.String("type")
	if kind == "assistant" || kind == "user" {
		return n.emitNormalized(message, func() json.RawMessage { return json.RawMessage("null") }, emit)
	}
	if kind != "progress" {
		return nil
	}
	var data protocol.JSONObject
	if err := data.UnmarshalJSON(message.Record.Get("data")); err != nil || !data.IsObject() {
		return cap("original progress data object")
	}
	dataKind := data.String("type")
	if dataKind == "agent_progress" || dataKind == "skill_progress" {
		nested, err := Decode(data.Get("message"))
		if err != nil {
			return err
		}
		return n.emitNormalized(nested, func() json.RawMessage { return message.Record.Get("parentToolUseID") }, emit)
	}
	if dataKind != "bash_progress" && dataKind != "powershell_progress" {
		return nil
	}
	if !envTruthy(n.deps.OriginalEnv("CLAUDE_CODE_REMOTE")) && n.deps.OriginalEnv("CLAUDE_CODE_CONTAINER_ID") == "" {
		return nil
	}
	keyRaw := message.Record.Get("parentToolUseID")
	key, err := progressKey(keyRaw)
	if err != nil {
		return err
	}
	now := n.deps.NowMillis()
	last := n.progress[key]
	if !(now-last >= 30000) {
		return nil
	}
	if len(n.progress) >= 100 {
		old := n.progressOrder[0]
		if old != "undefined" {
			delete(n.progress, old)
			n.progressOrder = n.progressOrder[1:]
		}
	}
	if _, exists := n.progress[key]; !exists {
		n.progressOrder = append(n.progressOrder, key)
	}
	n.progress[key] = now
	name := "Bash"
	if dataKind == "powershell_progress" {
		name = "PowerShell"
	}
	record := protocol.NewJSONObject()
	for _, field := range []protocol.JSONField{{Name: "type", Value: quote("tool_progress")}, {Name: "tool_use_id", Value: message.Record.Get("toolUseID")}, {Name: "tool_name", Value: quote(name)}, {Name: "parent_tool_use_id", Value: keyRaw}, {Name: "elapsed_time_seconds", Value: data.Get("elapsedTimeSeconds")}, {Name: "task_id", Value: data.Get("taskId")}, {Name: "session_id", Value: n.deps.SessionID()}, {Name: "uuid", Value: message.Record.Get("uuid")}} {
		record.Set(field.Name, field.Value)
	}
	return emit(&View{Record: record})
}
func progressKey(raw json.RawMessage) (string, error) {
	if raw == nil {
		return "undefined", nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "null", nil
	}
	if isString(raw) {
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return "", err
		}
		return "string:" + string(protocol.QuoteJavaScriptUTF16(units)), nil
	}
	return "", cap("progress Map key identity outside string/missing/null")
}
func (n *Normalizer) emitNormalized(message Message, parent func() json.RawMessage, emit func(*View) error) error {
	values, err := n.NormalizeMessages([]Message{message})
	if err != nil {
		return err
	}
	for _, view := range values {
		kind := view.Record.String("type")
		if kind != "assistant" && kind != "user" {
			continue
		}
		if kind == "assistant" {
			nonempty, err := nonempty(view)
			if err != nil {
				return err
			}
			if !nonempty {
				continue
			}
		}
		sdk, err := n.sdkView(view, parent())
		if err != nil {
			return err
		}
		if err = emit(sdk); err != nil {
			return err
		}
	}
	return nil
}

func sameUTF16(units []uint16, text string) bool {
	want, _ := protocol.DecodeJavaScriptJSONString(quote(text))
	if len(units) != len(want) {
		return false
	}
	for index, unit := range units {
		if unit != want[index] {
			return false
		}
	}
	return true
}
