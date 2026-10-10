package retrypolicy

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// EnumerableFailure retains original transport/worker error properties. A Go
// Error() string cannot recover the Source's enumerable cause metadata, so an
// unported native cause must stop rather than silently serialize as {}.
type EnumerableFailure interface {
	error
	EnumerableErrorJSON() (protocol.JSONObject, error)
}

func serializedSDKError(failure error) (json.RawMessage, error) {
	if nilFailure(failure) {
		return nil, nil
	}
	sdk := apiFailure(failure)
	if sdk == nil {
		original, ok := failure.(EnumerableFailure)
		if !ok {
			return nil, missingAttempt("enumerable native fault properties")
		}
		properties, err := original.EnumerableErrorJSON()
		if err != nil {
			return nil, err
		}
		return properties.MarshalJSON()
	}
	fields := protocol.NewJSONObject()
	if sdk.Status != nil {
		fields.Set("status", json.RawMessage(strconv.Itoa(*sdk.Status)))
	}
	if sdk.Headers != nil || sdk.OriginalHeaders != nil {
		var headers json.RawMessage
		var err error
		if sdk.OriginalHeaders != nil {
			headers, err = sdk.OriginalHeaders.MarshalJSON()
		} else {
			headers, err = serializedHeaders(sdk.Headers)
		}
		if err != nil {
			return nil, err
		}
		fields.Set("headers", headers)
		requestID := json.RawMessage(`null`)
		if sdk.RequestID != nil {
			requestID = quote(*sdk.RequestID)
		}
		fields.Set("requestID", requestID)
	}
	if len(sdk.Payload) > 0 {
		payload, err := protocol.ParseAndStringifyJavaScriptJSON(sdk.Payload)
		if err != nil {
			return nil, missingAttempt("validated SDK payload")
		}
		fields.Set("error", payload)
	}
	if !nilFailure(sdk.Cause) {
		cause, err := serializedSDKError(sdk.Cause)
		if err != nil {
			return nil, err
		}
		fields.Set("cause", cause)
	}
	return fields.MarshalJSON()
}

// Bun Headers.toJSON preserves its native header enumeration, which is not
// the sorted Headers iterator. A Go map cannot reconstruct the order of two
// original names. Until the HTTP metadata adapter retains that record, reject
// it explicitly rather than silently change the CLI/SDK serialized notice.
func serializedHeaders(headers http.Header) (json.RawMessage, error) {
	if len(headers) > 1 {
		return nil, missingAttempt("original Bun Headers JSON enumeration")
	}
	object := protocol.NewJSONObject()
	for name, values := range headers {
		var value json.RawMessage
		if strings.EqualFold(name, "set-cookie") {
			raw, _ := json.Marshal(values)
			value = protocol.StringifyValidatedJavaScriptJSON(raw)
		} else {
			value = quote(strings.Join(values, ", "))
		}
		object.Set(strings.ToLower(name), value)
	}
	return object.MarshalJSON()
}
func quote(value string) json.RawMessage {
	raw, _ := json.Marshal(value)
	return protocol.StringifyValidatedJavaScriptJSON(raw)
}
func noticeNumber(value float64) json.RawMessage {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return json.RawMessage(`null`)
	}
	raw := []byte(strconv.FormatFloat(value, 'g', -1, 64))
	return protocol.StringifyValidatedJavaScriptJSON(raw)
}

// Envelope is the actual model-independent SystemAPIErrorMessage record.
// Source diagnostic Error fields remain non-enumerable; original payload,
// request ID, cause properties, undefined/null and numeric behavior survive.
func (notice RetryNotice) Envelope() (protocol.JSONObject, error) {
	if notice.Error == nil || apiFailure(notice.Error) == nil {
		return protocol.JSONObject{}, missingAttempt("retry notice SDK error")
	}
	errorJSON, err := serializedSDKError(notice.Error)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	causeJSON, err := serializedSDKError(notice.Cause)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: quote(notice.Type)},
		protocol.JSONField{Name: "subtype", Value: quote(notice.Subtype)},
		protocol.JSONField{Name: "level", Value: quote(notice.Level)},
		protocol.JSONField{Name: "cause", Value: causeJSON},
		protocol.JSONField{Name: "error", Value: errorJSON},
		protocol.JSONField{Name: "retryInMs", Value: noticeNumber(notice.RetryInMillis)},
		protocol.JSONField{Name: "retryAttempt", Value: noticeNumber(notice.RetryAttempt)},
		protocol.JSONField{Name: "maxRetries", Value: noticeNumber(notice.MaxRetries)},
		protocol.JSONField{Name: "timestamp", Value: quote(notice.Timestamp)},
		protocol.JSONField{Name: "uuid", Value: quote(notice.UUID)},
	), nil
}
