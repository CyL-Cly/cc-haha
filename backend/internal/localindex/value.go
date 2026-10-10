package localindex

import (
	"encoding/json"
	"math"
)

func encodeValue(value any) (json.RawMessage, error) {
	if number, ok := value.(float64); ok && (math.IsInf(number, 0) || math.IsNaN(number)) {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(value)
}
func raw(v any) json.RawMessage { data, _ := encodeValue(v); return data }
func intValue(value json.RawMessage) int64 {
	var number float64
	_ = json.Unmarshal(value, &number)
	return int64(number)
}
