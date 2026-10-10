package requestmapping

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type CacheControlFactory func(querySource *string) (json.RawMessage, error)
type BreakpointEvent struct {
	TotalMessageCount              int
	CachingEnabled, SkipCacheWrite bool
}
type Options struct {
	QuerySource    *string
	SkipCacheWrite bool
	CacheControl   CacheControlFactory
	OnEvent        func(BreakpointEvent)
}

type MappingError struct {
	Name, Message string
	messageJSON   json.RawMessage
}

func (failure *MappingError) Error() string { return failure.Message }

// MessageJSON preserves lone UTF-16 units in model-visible error feedback.
// Error()/Message are UTF-8 display strings, which replace those units.
func (failure *MappingError) MessageJSON() json.RawMessage {
	return append(json.RawMessage(nil), failure.messageJSON...)
}

type MessageParam struct {
	Role    string
	Content *Value
}

// Value exposes the exact mapped JSON object while retaining content aliases.
// Undefined content is omitted, as in the original JavaScript return object.
func (message *MessageParam) Value() *Value {
	return objectValue(map[string]*Value{"role": literalString(message.Role), "content": message.Content}, "role", "content")
}
func (message *MessageParam) MarshalJSON() ([]byte, error) { return message.Value().MarshalJSON() }

func typeError(message string) error { return typeErrorUnits(utf16.Encode([]rune(message))) }
func typeErrorUnits(units []uint16) error {
	quoted := protocol.QuoteJavaScriptUTF16(units)
	var display string
	_ = json.Unmarshal(quoted, &display)
	return &MappingError{Name: "TypeError", Message: display, messageJSON: quoted}
}
func accessError(value *Value, expression string) error {
	kind := "null"
	if value == nil {
		kind = "undefined"
	}
	return typeError(kind + " is not an object (evaluating '" + expression + "')")
}

func envelopeContent(envelope *Value) (*Value, error) {
	if nullish(envelope) {
		return nil, accessError(envelope, "message.message")
	}
	message := envelope.Property("message")
	if nullish(message) {
		return nil, accessError(message, "message.message.content")
	}
	return message.Property("content"), nil
}

func cacheControl(options Options) (*Value, error) {
	if options.CacheControl == nil {
		return nil, errors.New("request mapping requires an explicit cache_control factory")
	}
	raw, err := options.CacheControl(options.QuerySource)
	if err != nil {
		return nil, err
	}
	return ParseValue(raw)
}

func MapUserMessage(envelope *Value, addCache, enabled bool, options Options) (*MessageParam, error) {
	return mapMessage(envelope, "user", addCache, enabled, options)
}

func MapAssistantMessage(envelope *Value, addCache, enabled bool, options Options) (*MessageParam, error) {
	return mapMessage(envelope, "assistant", addCache, enabled, options)
}

func mapMessage(envelope *Value, role string, addCache, enabled bool, options Options) (*MessageParam, error) {
	content, err := envelopeContent(envelope)
	if err != nil {
		return nil, err
	}
	if !addCache {
		if role == "user" && content != nil && content.kind == '[' {
			content = arrayValue(append([]*Value{}, content.elements...))
		}
		return &MessageParam{Role: role, Content: content}, nil
	}
	if content != nil && content.kind == '"' {
		block := objectValue(map[string]*Value{"type": literalString("text"), "text": content}, "type", "text")
		if enabled {
			control, err := cacheControl(options)
			if err != nil {
				return nil, err
			}
			block.SetProperty("cache_control", control)
		}
		return &MessageParam{Role: role, Content: arrayValue([]*Value{block})}, nil
	}
	if nullish(content) {
		return nil, accessError(content, "message.message.content.map")
	}
	if content.kind != '[' {
		return nil, mapNotFunctionError(content, role)
	}
	blocks := make([]*Value, 0, len(content.elements))
	for index, block := range content.elements {
		cloned := spread(block)
		if index == len(content.elements)-1 {
			canCache := true
			if role == "assistant" {
				if nullish(block) {
					return nil, accessError(block, "_.type")
				}
				canCache = !stringEquals(block.Property("type"), "thinking") && !stringEquals(block.Property("type"), "redacted_thinking")
			}
			if canCache && enabled {
				control, err := cacheControl(options)
				if err != nil {
					return nil, err
				}
				cloned.SetProperty("cache_control", control)
			}
		}
		blocks = append(blocks, cloned)
	}
	return &MessageParam{Role: role, Content: arrayValue(blocks)}, nil
}

// AddCacheBreakpoints is the actual release's useCachedMC=false branch. The
// marker targets the last message, or the second-last for skipCacheWrite, and
// existing cache_control fields are preserved everywhere else.
func AddCacheBreakpoints(messages []*Value, enabled bool, options Options) ([]*MessageParam, error) {
	if options.OnEvent != nil {
		options.OnEvent(BreakpointEvent{TotalMessageCount: len(messages), CachingEnabled: enabled, SkipCacheWrite: options.SkipCacheWrite})
	}
	marker := len(messages) - 1
	if options.SkipCacheWrite {
		marker--
	}
	result := make([]*MessageParam, 0, len(messages))
	for index, message := range messages {
		if nullish(message) {
			return nil, accessError(message, "msg.type")
		}
		role := "assistant"
		if stringEquals(message.Property("type"), "user") {
			role = "user"
		}
		mapped, err := mapMessage(message, role, index == marker, enabled, options)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

// Bun's pinned TypeError contains the source expression, including the
// default-release elimination of CONNECTOR_TEXT. Keep the observed message.
func mapNotFunctionError(content *Value, role string) error {
	condition := "i === message.message.content.length - 1"
	if role == "assistant" {
		condition += " && _.type !== \"thinking\" && _.type !== \"redacted_thinking\""
	}
	description := "undefined"
	var stringUnits []uint16
	if property := content.Property("map"); property != nil {
		switch property.kind {
		case '{':
			description = "an instance of Object"
		case '[':
			description = "an instance of Array"
		case '"':
			units, _ := protocol.DecodeJavaScriptJSONString(property.raw)
			stringUnits = append(append([]uint16{'"'}, units...), '"')
		default:
			description, _ = protocol.JoinJavaScriptValues([]json.RawMessage{property.raw}, "")
		}
		if property.kind == 'n' {
			description = "null"
		}
	}
	prefix := fmt.Sprintf("message.message.content.map is not a function. (In 'message.message.content.map((_, i) => ({\n          ..._,\n          ...%s ? enablePromptCaching ? { cache_control: getCacheControl({ querySource }) } : {} : {}\n        }))', 'message.message.content.map' is ", condition)
	if stringUnits == nil {
		stringUnits = utf16.Encode([]rune(description))
	}
	units := append(utf16.Encode([]rune(prefix)), stringUnits...)
	return typeErrorUnits(append(units, ')'))
}
