package providerproxy

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ResponsesOptions struct {
	PreserveOpenAIReasoning bool
	NowMillis               func() int64
}
type ResponseError struct {
	Message, Type, Code string
	Status              int
	Name                string
	MessageJSON         json.RawMessage
}

func (e *ResponseError) Error() string { return e.Message }
func (e *ResponseError) ErrorName() string {
	if e.Name != "" {
		return e.Name
	}
	return "Error"
}
func responseError(message string) *ResponseError {
	return &ResponseError{Message: message, MessageJSON: responseJSON(message)}
}
func responseJSON(value any) json.RawMessage {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(value) != nil {
		return nil
	}
	return protocol.StringifyValidatedJavaScriptJSON(bytes.TrimSpace(buffer.Bytes()))
}
func responseField(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: responseJSON(value)}
}
func responseRawField(name string, value json.RawMessage) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: value}
}
func responseObject(value json.RawMessage) protocol.JSONObject {
	var record protocol.JSONObject
	_ = record.UnmarshalJSON(value)
	return record
}
func responseRecord(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func responseString(value json.RawMessage) bool { return len(value) > 0 && value[0] == '"' }
func responseTruthy(value json.RawMessage) bool {
	if len(value) == 0 || string(value) == "null" || string(value) == "false" || string(value) == `""` {
		return false
	}
	if value[0] == '"' || value[0] == '[' || value[0] == '{' || string(value) == "true" {
		return true
	}
	n, _ := strconv.ParseFloat(string(value), 64)
	return n != 0 && !math.IsNaN(n)
}
func responseText(value json.RawMessage) string {
	var text string
	_ = json.Unmarshal(value, &text)
	return text
}
func responseUnits(value json.RawMessage) []uint16 {
	units, _ := protocol.DecodeJavaScriptJSONString(value)
	return units
}
func responseConcat(values ...json.RawMessage) json.RawMessage {
	joined, err := protocol.JoinJavaScriptValuesJSON(values, "")
	if err != nil {
		panic(err)
	}
	return joined
}
func responseNow(clock func() int64) int64 {
	if clock != nil {
		return clock()
	}
	return time.Now().UnixMilli()
}
func responseFallbackID(clock func() int64) json.RawMessage {
	return responseJSON("msg_" + strconv.FormatInt(responseNow(clock), 10))
}
func responseArray(value json.RawMessage) bool   { return len(value) > 0 && value[0] == '[' }
func responseNullish(value json.RawMessage) bool { return len(value) == 0 || string(value) == "null" }
