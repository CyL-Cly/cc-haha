package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestmapping"
)

// MessagesRequest starts after request policy has resolved the params. Body is
// the final signed JSON wire representation: transports must send it unchanged.
// RemovedHeaders are explicit caller omissions, applied after transport defaults.
type MessagesRequest struct {
	Body           []byte
	Path           string
	Headers        http.Header
	RemovedHeaders []string
	deferCCH       bool
}

// HeaderEntry.Values contains one nil for a null omission, no entries for an
// undefined/empty array, and ordered strings for ordinary/array header values.
type HeaderEntry struct {
	Name   string
	Values []*string
}

type BetaMessageOptions struct {
	// A host with an actual OAuth client consumer must wait for selection.
	// Normal fetch signs CCH; the original OpenAI wrapper never runs buildFetch.
	DeferCCH    bool
	Headers     []HeaderEntry
	HeaderKind  string   // empty/object, tuples, or Headers (already flattened).
	HelperNames []string // Non-JSON SDK helper symbol identities, in scan order.
}

const outputFormatConflict = "Both output_format and output_config.format were provided. Please use only output_config.format (output_format is deprecated)."

// ErrUnsupportedMessageEncoding marks an unported SDK encoder branch, rather
// than silently sending signed JSON for a request the SDK form-encodes.
var ErrUnsupportedMessageEncoding = errors.New("beta.messages form-urlencoded request encoding is not migrated")

// Arbitrary SDK JavaScript arguments are not the resolved params object seam.
var ErrUnsupportedMessageParams = errors.New("beta.messages unresolved non-object params are not migrated")

// PrepareBetaMessages mirrors SDK 0.80.0 beta.messages.create for resolved
// plain-JSON object params. It preserves input references and property order. Policy,
// SDK helper-symbol discovery, authentication defaults and validation, timeout,
// response/retry policy and non-JSON encoders remain outside this component.
func PrepareBetaMessages(params *requestmapping.Value, options BetaMessageOptions) (*MessagesRequest, error) {
	body, err := prepareBetaMessageBody(params)
	if err != nil {
		return nil, err
	}
	result := &MessagesRequest{Body: []byte(SignClaudeCodeCCHInString(string(body))), Path: "/v1/messages?beta=true", Headers: http.Header{}}
	if options.DeferCCH {
		result.Body, result.deferCCH = append([]byte(nil), body...), true
	}
	removed := map[string]bool{}
	if betas := params.Property("betas"); betas != nil && !bytes.Equal(valueRaw(betas), []byte("null")) {
		beta, _ := protocol.JoinJavaScriptValues([]json.RawMessage{coercionRaw(betas)}, "")
		result.Headers.Set("anthropic-beta", beta)
	}
	seen := map[string]bool{}
	var helpers []string
	for _, helper := range options.HelperNames {
		if !seen[helper] {
			seen[helper] = true
			helpers = append(helpers, helper)
		}
	}
	if len(helpers) > 0 {
		result.Headers.Set("x-stainless-helper", strings.Join(helpers, ", "))
	}
	entries := options.Headers
	if options.HeaderKind == "" || options.HeaderKind == "object" {
		// Repeated exact keys are one object property with its original slot.
		var object []HeaderEntry
		positions := map[string]int{}
		for _, entry := range entries {
			if index, exists := positions[entry.Name]; exists {
				object[index] = entry
			} else {
				positions[entry.Name] = len(object)
				object = append(object, entry)
			}
		}
		entries = object
	}
	seen = map[string]bool{}
	for _, entry := range entries {
		name := strings.ToLower(entry.Name)
		for index, value := range entry.Values {
			if !seen[name] || (index == 0 && (options.HeaderKind == "" || options.HeaderKind == "object")) {
				result.Headers.Del(name)
				seen[name] = true
			}
			if value == nil {
				result.Headers.Del(name)
				removed[name] = true
			} else {
				trimmed := strings.Trim(*value, "\t\r\n ")
				if prior, exists := result.Headers[http.CanonicalHeaderKey(name)]; exists {
					result.Headers.Set(name, prior[0]+", "+trimmed)
				} else {
					result.Headers.Set(name, trimmed)
				}
				delete(removed, name)
			}
		}
	}
	for name := range removed {
		result.RemovedHeaders = append(result.RemovedHeaders, name)
	}
	sort.Strings(result.RemovedHeaders)
	if result.Headers.Get("content-type") == "application/x-www-form-urlencoded" {
		return nil, ErrUnsupportedMessageEncoding
	}
	return result, nil
}

func prepareBetaMessageBody(params *requestmapping.Value) ([]byte, error) {
	serialized, _ := params.MarshalJSON()
	if len(serialized) == 0 || serialized[0] != '{' {
		return nil, ErrUnsupportedMessageParams
	}
	format := params.Property("output_format")
	formatRaw := valueRaw(format)
	transform := jsonTruthy(formatRaw)
	config := params.Property("output_config")
	if transform && jsonTruthy(valueRaw(config.Property("format"))) {
		return nil, errors.New(outputFormatConflict)
	}
	fields := readMessageFields(serialized)
	if transform {
		fields = removeMessageField(fields, "output_format")
		configFields := spreadMessageFields(config)
		configFields = setMessageField(configFields, "format", formatRaw)
		fields = setMessageField(fields, "output_config", marshalMessageFields(configFields))
	}
	fields = removeMessageField(fields, "betas")
	body, err := protocol.ParseAndStringifyJavaScriptJSON(marshalMessageFields(fields))
	if err != nil {
		return nil, err
	}
	return body, nil
}

type messageField struct {
	key    string
	quoted json.RawMessage
	value  json.RawMessage
}

// Only reserved ASCII fields are selected or overwritten here. Unknown quoted
// keys remain opaque, including distinct lone UTF-16 keys, without map sorting.
func readMessageFields(raw []byte) []messageField {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token()
	var fields []messageField
	for decoder.More() {
		start := decoder.InputOffset()
		key, _ := decoder.Token()
		quoted := raw[start:decoder.InputOffset()]
		quoted = quoted[bytes.IndexByte(quoted, '"'):]
		var value json.RawMessage
		_ = decoder.Decode(&value)
		fields = append(fields, messageField{key: key.(string), quoted: quoted, value: value})
	}
	return fields
}

func removeMessageField(fields []messageField, key string) []messageField {
	var result []messageField
	for _, field := range fields {
		if field.key != key {
			result = append(result, field)
		}
	}
	return result
}

func setMessageField(fields []messageField, key string, value json.RawMessage) []messageField {
	for index := range fields {
		if fields[index].key == key {
			fields[index].value = value
			return fields
		}
	}
	quoted, _ := json.Marshal(key)
	return append(fields, messageField{key: key, quoted: quoted, value: value})
}

func marshalMessageFields(fields []messageField) []byte {
	result := []byte{'{'}
	for index, field := range fields {
		if index > 0 {
			result = append(result, ',')
		}
		result = append(result, field.quoted...)
		result = append(result, ':')
		result = append(result, field.value...)
	}
	return append(result, '}')
}

func valueRaw(value *requestmapping.Value) json.RawMessage {
	if value == nil {
		return nil
	}
	raw, _ := value.MarshalJSON()
	return raw
}

func spreadMessageFields(value *requestmapping.Value) []messageField {
	raw := valueRaw(value)
	if len(raw) == 0 {
		return nil
	}
	switch raw[0] {
	case '{':
		return readMessageFields(raw)
	case '[':
		length, _ := value.ArrayLength()
		var fields []messageField
		for index := 0; index < length; index++ {
			fields = setMessageField(fields, strconv.Itoa(index), valueRaw(value.Element(index)))
		}
		return fields
	case '"':
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		var fields []messageField
		for index, unit := range units {
			fields = setMessageField(fields, strconv.Itoa(index), protocol.QuoteJavaScriptUTF16([]uint16{unit}))
		}
		return fields
	default:
		return nil
	}
}

// Preserve numeric values until String coercion; Value.MarshalJSON for a whole
// array would first stringify Infinity to null, changing beta header text.
func coercionRaw(value *requestmapping.Value) json.RawMessage {
	if length, array := value.ArrayLength(); array {
		result := []byte{'['}
		for index := 0; index < length; index++ {
			if index > 0 {
				result = append(result, ',')
			}
			result = append(result, coercionRaw(value.Element(index))...)
		}
		return append(result, ']')
	}
	return valueRaw(value)
}
