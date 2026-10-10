package apipresentation

import (
	"encoding/json"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

// FormatAPIErrorText is the original errorUtils.formatAPIError consumer used
// outside the main API presentation. It preserves actual non-enumerable Error
// message/cause fields rather than reconstructing them from a JSONL snapshot.
func FormatAPIErrorText(failure error) (json.RawMessage, error) {
	code, e := connectionCode(failure)
	if e != nil {
		return nil, e
	}
	name := ""
	if code != nil {
		units, _ := protocol.DecodeJavaScriptJSONString(code)
		name = string(utf16.Decode(units))
	}
	switch name {
	case "ETIMEDOUT", "UNABLE_TO_VERIFY_LEAF_SIGNATURE", "UNABLE_TO_GET_ISSUER_CERT", "UNABLE_TO_GET_ISSUER_CERT_LOCALLY", "CERT_SIGNATURE_FAILURE", "CERT_NOT_YET_VALID", "CERT_HAS_EXPIRED", "CERT_REVOKED", "CERT_REJECTED", "CERT_UNTRUSTED", "DEPTH_ZERO_SELF_SIGNED_CERT", "SELF_SIGNED_CERT_IN_CHAIN", "CERT_CHAIN_TOO_LONG", "PATH_LENGTH_EXCEEDED", "ERR_TLS_CERT_ALTNAME_INVALID", "HOSTNAME_MISMATCH", "ERR_TLS_HANDSHAKE_TIMEOUT", "ERR_SSL_WRONG_VERSION_NUMBER", "ERR_SSL_DECRYPTION_FAILED_OR_BAD_RECORD_MAC":
		value := connectionText(code)
		units, _ := protocol.DecodeJavaScriptJSONString(value)
		return protocol.QuoteJavaScriptUTF16(units[len("API Error: "):]), nil
	}
	var message json.RawMessage
	var payload json.RawMessage
	status := json.RawMessage(`"unknown"`)
	switch value := failure.(type) {
	case *provider.SDKStreamError:
		if value == nil {
			return nil, unsupported("actual APIError instance")
		}
		message = value.MessageJSON
		if message == nil {
			message = quote(value.Message)
		}
		payload = value.Payload
		if value.Status != nil {
			status = []byte(strconv.Itoa(*value.Status))
		}
	case *OriginalError:
		if value != nil {
			message = value.message
		}
	default:
		if value, ok := failure.(interface{ ErrorMessageJSON() json.RawMessage }); ok {
			message = value.ErrorMessageJSON()
		} else if failure != nil {
			message = quote(failure.Error())
		}
	}
	return formatAPIErrorValues(message, payload, status, code)
}

// A serialized record has plain JSON objects, so Source does not traverse its
// cause as Error instances. Nested payload/message/status still keep raw units.
func FormatAPIErrorRecord(record protocol.JSONObject) (json.RawMessage, error) {
	status := record.Get("status")
	if status == nil || string(status) == "null" {
		status = json.RawMessage(`"unknown"`)
	}
	return formatAPIErrorValues(record.Get("message"), record.Get("error"), status, nil)
}
func formatAPIErrorValues(message, payload, status, code json.RawMessage) (json.RawMessage, error) {
	if len(message) > 0 {
		units, e := protocol.DecodeJavaScriptJSONString(message)
		if e != nil {
			return nil, e
		}
		if len(units) > 0 {
			if string(utf16.Decode(units)) == "Connection error." {
				value := connectionText(code)
				u, _ := protocol.DecodeJavaScriptJSONString(value)
				return protocol.QuoteJavaScriptUTF16(u[len("API Error: "):]), nil
			}
			sanitized := sanitizeErrorHTML(units)
			if len(sanitized) > 0 {
				return protocol.QuoteJavaScriptUTF16(sanitized), nil
			}
			return protocol.QuoteJavaScriptUTF16(units), nil
		}
	}
	object := protocol.NewJSONObject()
	_ = object.UnmarshalJSON(payload)
	inner := protocol.NewJSONObject()
	_ = inner.UnmarshalJSON(object.Get("error"))
	for _, raw := range []json.RawMessage{inner.Get("message"), object.Get("message")} {
		if units, e := protocol.DecodeJavaScriptJSONString(raw); e == nil && len(units) > 0 {
			if sanitized := sanitizeErrorHTML(units); len(sanitized) > 0 {
				return protocol.QuoteJavaScriptUTF16(sanitized), nil
			}
		}
	}
	value, e := protocol.JavaScriptStringJSON(status)
	if e != nil {
		return nil, e
	}
	return interpolate("API error (status ", value, ")"), nil
}
func sanitizeErrorHTML(units []uint16) []uint16 {
	index := func(needle string, from int) int {
		values := utf16.Encode([]rune(needle))
		for i := from; i+len(values) <= len(units); i++ {
			match := true
			for j, v := range values {
				if units[i+j] != v {
					match = false
					break
				}
			}
			if match {
				return i
			}
		}
		return -1
	}
	if index("<!DOCTYPE html", 0) < 0 && index("<html", 0) < 0 {
		return units
	}
	for begin := index("<title>", 0); begin >= 0; begin = index("<title>", begin+1) {
		start := begin + len("<title>")
		end := start
		for end < len(units) && units[end] != '<' {
			end++
		}
		if end == start || end+len("</title>") > len(units) {
			continue
		}
		if index("</title>", end) != end {
			continue
		}
		a, b := start, end
		for a < b && protocol.IsJavaScriptWhitespace(rune(units[a])) {
			a++
		}
		for b > a && protocol.IsJavaScriptWhitespace(rune(units[b-1])) {
			b--
		}
		return units[a:b]
	}
	return nil
}
