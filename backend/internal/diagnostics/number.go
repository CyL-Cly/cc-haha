package diagnostics

import (
	"encoding/json"
	"math"
	"strconv"
)

// Number retains JSON.stringify's non-finite/null and negative-zero behavior.
// Final ordered entry encoding uses the existing exact JavaScript JSON codec.
func Number(value float64) json.RawMessage {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return json.RawMessage("null")
	}
	if value == 0 {
		return json.RawMessage("0")
	}
	return json.RawMessage(strconv.FormatFloat(value, 'g', -1, 64))
}
