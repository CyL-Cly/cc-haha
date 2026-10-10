package apipresentation

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"regexp"
	"strings"
	"unicode/utf16"
)

// OriginalError represents an original Error(message) value supplied by a host
// Throw operation. It does not turn arbitrary native errors into JS instances.
type OriginalError struct{ message json.RawMessage }

func NewOriginalError(message json.RawMessage) (*OriginalError, error) {
	if _, e := protocol.DecodeJavaScriptJSONString(message); e != nil {
		return nil, unsupported("original Error message string")
	}
	return &OriginalError{message: append(json.RawMessage(nil), message...)}, nil
}
func (e *OriginalError) Error() string {
	if e == nil {
		return "nil original Error"
	}
	u, _ := protocol.DecodeJavaScriptJSONString(e.message)
	return string(utf16.Decode(u))
}

var specialOrdinary = regexp.MustCompile(`(?i)prompt is too long|input is too long for requested model|context_length_exceeded|maximum context length|exceeds? the context window|context window exceeded|supports only [0-9]+\s*k?\s*(tokens?\s+of\s+)?context|maximum of [0-9]+ PDF pages|invalid model name|x-api-key`)

func finiteOrdinary(message string) error {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "image") {
		for _, part := range []string{"not support", "unsupported", "vision", "multimodal", "multi-modal", "modality"} {
			if strings.Contains(lower, part) {
				return unsupported("specialized stream image precedence")
			}
		}
		if strings.Contains(lower, "image_url") {
			for _, part := range []string{"not allowed", "not permitted", "disallowed", "forbidden"} {
				if strings.Contains(lower, part) {
					return unsupported("specialized stream image precedence")
				}
			}
			if strings.Contains(lower, "text") {
				for _, part := range []string{"expected", "input should be", "not one of", "permitted", "received", "unknown variant", "invalid value", "invalid type", "valid enumeration", "only text"} {
					if strings.Contains(lower, part) {
						return unsupported("specialized stream image precedence")
					}
				}
			}
		}
	}

	if specialOrdinary.MatchString(message) || strings.Contains(message, "Opus is experiencing high load, please use /model to switch to Sonnet") || strings.Contains(message, "The PDF specified is password protected") || strings.Contains(message, "The PDF specified was not valid") || strings.Contains(message, "Your credit balance is too low") {
		return unsupported("specialized stream error presentation precedence")
	}
	return nil
}
func apiDetail(sdk *provider.SDKStreamError) (json.RawMessage, error) {
	raw := sdk.MessageJSON
	if raw == nil {
		raw = quote(sdk.Message)
	}
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return nil, e
	}
	body := sdk.Payload
	if body == nil || string(body) == "null" {
		text := string(utf16.Decode(units))
		if regexp.MustCompile(`^\s*[\[{\"]`).MatchString(text) {
			body = []byte(text)
			if !json.Valid(body) {
				body = nil
			}
		}
	}
	detail := raw
	if body != nil && string(body) != "null" {
		detail = quote("Request failed")
		current := body
		for depth := 0; depth < 5; depth++ {
			if _, e := protocol.DecodeJavaScriptJSONString(current); e == nil {
				detail = current
				break
			}
			var record protocol.JSONObject
			if e := record.UnmarshalJSON(current); e != nil || !record.IsObject() {
				break
			}
			if message := record.Get("message"); message != nil {
				if _, e := protocol.DecodeJavaScriptJSONString(message); e == nil {
					detail = message
					break
				}
			}
			current = record.Get("error")
		}
	}
	u, e := protocol.DecodeJavaScriptJSONString(detail)
	if e != nil {
		return nil, e
	}
	text := string(utf16.Decode(u))
	if regexp.MustCompile(`(?i)^\s*(<!doctype\s+html\b|<html\b|<head\b|<body\b)`).MatchString(text) {
		return quote("Received an HTML error response from the server."), nil
	}
	// ECMAScript trimEnd operates on code units and preserves lone surrogates.
	for len(u) > 0 && jsTrailingSpace(u[len(u)-1]) {
		u = u[:len(u)-1]
	}
	return protocol.QuoteJavaScriptUTF16(u), nil
}
func jsTrailingSpace(u uint16) bool {
	switch u {
	case 9, 10, 11, 12, 13, 32, 0xa0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return false
}

// StreamFailure is the Source getAssistantMessageFromError ordinary fallback
// domain. Non-status SSE APIError and original plain Error are represented;
// specialized media/auth/overflow/status/native classes remain explicit caps.
func StreamFailure(failure error, domain Domain, factory *messages.Factory) (*streamhistory.Envelope, error) {
	if !domain.direct || factory == nil || absent(failure) {
		return nil, unsupported("original stream presentation domain")
	}
	var detail json.RawMessage
	switch original := failure.(type) {
	case *OriginalError:
		if original == nil {
			return nil, unsupported("nil original Error")
		}
		if e := finiteOrdinary(original.Error()); e != nil {
			return nil, e
		}
		detail = original.message
	case *provider.SDKStreamError:
		if original == nil || original.Class != "APIError" || original.Kind != "sse_api" || original.Status != nil {
			return nil, unsupported("stream SDK class/status")
		}
		if e := finiteOrdinary(original.Message); e != nil {
			return nil, e
		}
		var e error
		detail, e = apiDetail(original)
		if e != nil {
			return nil, e
		}
	default:
		return nil, unsupported("unrepresented stream Error class")
	}
	record, e := factory.CreateOrderedAssistantAPIError(storage.Object{"content": interpolate("API Error: ", detail, ""), "error": quote("unknown")})
	if e != nil {
		return nil, e
	}
	var message protocol.JSONObject
	if e = message.UnmarshalJSON(record.Get("message")); e != nil {
		return nil, e
	}
	var blocks []protocol.JSONObject
	if e = json.Unmarshal(message.Get("content"), &blocks); e != nil {
		return nil, e
	}
	content := make([]*streamhistory.Block, 0, len(blocks))
	for _, b := range blocks {
		content = append(content, &streamhistory.Block{Object: b})
	}
	return &streamhistory.Envelope{Record: record, Message: message, Content: content, Usage: &streamhistory.Usage{Raw: message.Get("usage")}, UUID: record.String("uuid"), APIError: true}, nil
}

// Only the native body's proven Bun socket failure is converted here; arbitrary
// host/admin errors keep their original admission boundary.
func NativeStreamFailure(failure error, domain Domain, factory *messages.Factory) (*streamhistory.Envelope, error) {
	if native, ok := failure.(*provider.NativeStreamReadError); ok {
		original, e := NewOriginalError(quote(native.Error()))
		if e != nil {
			return nil, e
		}
		return StreamFailure(original, domain, factory)
	}
	return StreamFailure(failure, domain, factory)
}
