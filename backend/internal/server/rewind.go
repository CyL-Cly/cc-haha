package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

func sourceJSONTruthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if raw[0] == '"' {
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		return err == nil && len(units) != 0
	}
	if raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9' {
		value, err := strconv.ParseFloat(string(raw), 64)
		return (err == nil || math.IsInf(value, 0)) && value != 0 && !math.IsNaN(value)
	}
	return true
}

func rewindBodyIndex(raw json.RawMessage) *int {
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
		return nil
	}
	maximum := int(^uint(0) >> 1)
	index := int(value)
	if value >= float64(maximum) {
		index = maximum
	} else if value < float64(-maximum) {
		index = -maximum
	}
	return &index
}

func (server *Server) rewindHTTP(writer http.ResponseWriter, request *http.Request, info sessionInfo) error {
	if request.Method != http.MethodPost {
		return methodNotAllowed(request.Method)
	}
	body, err := jsonObject(request)
	if err != nil {
		return err
	}
	selector := sessionrewind.Selector{TargetUserMessageID: stringField(body, "targetUserMessageId"), UserMessageIndex: rewindBodyIndex(body["userMessageIndex"])}
	if selector.TargetUserMessageID == "" && selector.UserMessageIndex == nil {
		return badRequest("targetUserMessageId (string) or userMessageIndex (integer) is required")
	}
	if raw, exists := body["expectedContent"]; exists {
		var expected string
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &expected) != nil {
			// Source calls string.replace on this field rather than accepting a
			// non-string as an omitted check. Keep that failure observable.
			return fmt.Errorf("rewind expectedContent must support string replacement")
		}
		selector.ExpectedContent = &expected
	}
	mode, err := sessionrewind.ParseMode(body["mode"])
	if err != nil {
		return checkpointError(err)
	}
	if server.checkpoints == nil {
		return unsupported("session rewind recorded shell classifier binding")
	}
	if sourceJSONTruthy(body["dryRun"]) {
		result, err := server.checkpoints.Preview(request.Context(), info.ID, selector)
		if err != nil {
			return checkpointError(err)
		}
		writeJSON(writer, 200, result)
		return nil
	}
	result, err := server.checkpoints.Execute(request.Context(), info.ID, selector, mode)
	if err != nil {
		return checkpointError(err)
	}
	writeJSON(writer, 200, result)
	return nil
}
