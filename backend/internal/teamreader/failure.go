package teamreader

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
)

func memberFailure(entry protocol.JSONObject) (json.RawMessage, json.RawMessage) {
	var lastError json.RawMessage
	if value := entry.Get("lastError"); len(value) > 0 && value[0] == '"' && protocol.TrimJavaScriptString(entry.String("lastError")) != "" {
		lastError = append(json.RawMessage(nil), value...)
	}
	var retry json.RawMessage
	r := obj(entry.Get("autoRetry"))
	if r.IsObject() {
		valid := true
		for _, key := range []string{"attempt", "max", "nextAt"} {
			var number float64
			value := r.Get(key)
			if len(value) == 0 || string(value) == "null" || json.Unmarshal(value, &number) != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				valid = false
			}
		}
		if valid {
			retry = raw(protocol.NewJSONObject(protocol.JSONField{Name: "attempt", Value: r.Get("attempt")}, protocol.JSONField{Name: "max", Value: r.Get("max")}, protocol.JSONField{Name: "nextAt", Value: r.Get("nextAt")}))
		}
	}
	return lastError, retry
}
