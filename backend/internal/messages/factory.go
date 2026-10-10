// Package messages builds the shared Agent history envelopes. These defaults
// come from utils/messages.ts and are independent of interactive/SDK policies.
package messages

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

const NoContent = "(no content)"
const SyntheticModel = "<synthetic>"
const Interrupt = "[Request interrupted by user]"
const InterruptForToolUse = "[Request interrupted by user for tool use]"

type Factory struct {
	uuid func() string
	now  func() time.Time
}

func NewFactory(uuid func() string, now func() time.Time) (*Factory, error) {
	if uuid == nil || now == nil {
		return nil, errors.New("message factory requires session UUID and clock sources")
	}
	return &Factory{uuid: uuid, now: now}, nil
}

func stringJSON(value string) json.RawMessage         { raw, _ := json.Marshal(value); return raw }
func objectJSON(value storage.Object) json.RawMessage { raw, _ := json.Marshal(value); return raw }

func truthy(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || bytes.Equal(value, []byte("false")) {
		return false
	}
	switch value[0] {
	case '"':
		var text string
		_ = json.Unmarshal(value, &text)
		return text != ""
	case '{', '[', 't':
		return true
	default:
		number, _ := strconv.ParseFloat(string(value), 64)
		return number != 0 && !math.IsNaN(number)
	}
}

func (factory *Factory) timestamp() json.RawMessage {
	// JS Date has millisecond precision and ISO output always uses UTC.
	return stringJSON(time.UnixMilli(factory.now().UnixMilli()).UTC().Format("2006-01-02T15:04:05.000Z"))
}

func copyFields(to, from storage.Object, fields ...string) {
	for _, field := range fields {
		if raw, exists := from[field]; exists && raw != nil {
			to[field] = append(json.RawMessage(nil), raw...)
		}
	}
}

// Options use raw JSON for the history fields that must retain UTF-16 strings,
// optional/null distinctions, and future nested MCP/tool metadata.
func (factory *Factory) CreateUser(options storage.Object) storage.Object {
	content := options["content"]
	if !truthy(content) {
		content = stringJSON(NoContent)
	}
	api := storage.Object{"role": stringJSON("user"), "content": content}
	uuid := options["uuid"]
	if !truthy(uuid) {
		uuid = stringJSON(factory.uuid())
	}
	timestamp := options["timestamp"]
	if len(timestamp) == 0 || bytes.Equal(bytes.TrimSpace(timestamp), []byte("null")) {
		timestamp = factory.timestamp()
	}
	message := storage.Object{"type": stringJSON("user"), "message": objectJSON(api), "uuid": uuid, "timestamp": timestamp}
	copyFields(message, options, "isMeta", "isVisibleInTranscriptOnly", "isVirtual", "isCompactSummary", "summarizeMetadata", "toolUseResult", "mcpMeta", "imagePasteIds", "sourceToolAssistantUUID", "permissionMode", "origin")
	return message
}

var defaultUsage = json.RawMessage(`{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":null,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":null,"iterations":null,"speed":null}`)

func assistantContent(content json.RawMessage) json.RawMessage {
	value := bytes.TrimSpace(content)
	if len(value) > 0 && value[0] == '"' {
		var text string
		_ = json.Unmarshal(content, &text)
		if text == "" {
			content = stringJSON(NoContent)
		}
		block, _ := textBlock(content).MarshalJSON()
		return append(append(json.RawMessage{'['}, block...), ']')
	}
	return content
}

func (factory *Factory) assistant(options storage.Object, apiError bool) storage.Object {
	// The reference evaluates envelope UUID/time before the API message ID.
	uuid := stringJSON(factory.uuid())
	timestamp := factory.timestamp()
	id := stringJSON(factory.uuid())
	usage := options["usage"]
	if usage == nil {
		usage = append(json.RawMessage(nil), defaultUsage...)
	}
	api := storage.Object{"id": id, "container": json.RawMessage("null"), "model": stringJSON(SyntheticModel), "role": stringJSON("assistant"), "stop_reason": stringJSON("stop_sequence"), "stop_sequence": stringJSON(""), "type": stringJSON("message"), "usage": usage, "context_management": json.RawMessage("null")}
	if content := assistantContent(options["content"]); content != nil {
		api["content"] = content
	}
	message := storage.Object{"type": stringJSON("assistant"), "uuid": uuid, "timestamp": timestamp, "message": objectJSON(api), "isApiErrorMessage": json.RawMessage(strconv.FormatBool(apiError))}
	if apiError {
		copyFields(message, options, "apiError", "error", "errorDetails", "businessErrorCode", "sourceModel")
	} else {
		copyFields(message, options, "isVirtual")
	}
	return message
}

func (factory *Factory) CreateAssistant(options storage.Object) storage.Object {
	return factory.assistant(options, false)
}
func (factory *Factory) CreateAssistantAPIError(options storage.Object) storage.Object {
	filtered := storage.Object{}
	copyFields(filtered, options, "content", "apiError", "error", "errorDetails", "businessErrorCode", "sourceModel")
	return factory.assistant(filtered, true)
}

func (factory *Factory) CreateUserInterruption(toolUse bool) storage.Object {
	text := Interrupt
	if toolUse {
		text = InterruptForToolUse
	}
	content := assistantContent(stringJSON(text))
	return factory.CreateUser(storage.Object{"content": content})
}

func PrepareUserContent(input json.RawMessage, preceding []json.RawMessage) json.RawMessage {
	if len(preceding) == 0 {
		return append(json.RawMessage(nil), input...)
	}
	blocks := append([]json.RawMessage(nil), preceding...)
	blocks = append(blocks, objectJSON(storage.Object{"text": input, "type": stringJSON("text")}))
	raw, _ := json.Marshal(blocks)
	return raw
}
