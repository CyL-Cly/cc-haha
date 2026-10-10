package insights

import (
	"encoding/json"
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// A live NaN must remain distinct from persisted JSON null. Cached metadata is
// intentionally unvalidated in Source, so += can concatenate numeric strings.
type jsSum struct {
	value json.RawMessage
	nan   bool
}

func (s jsSum) number() float64 {
	if s.nan {
		return math.NaN()
	}
	if s.value == nil {
		return 0
	}
	return jsNumber(s.value)
}
func (s jsSum) text() json.RawMessage {
	if s.nan {
		return raw("NaN")
	}
	if s.value == nil {
		return raw("0")
	}
	v, _ := protocol.JavaScriptStringJSON(s.value)
	return v
}
func (s jsSum) plus(v json.RawMessage) jsSum {
	if v != nil && (v[0] == '[' || v[0] == '{') {
		v, _ = protocol.JavaScriptStringJSON(v)
	}
	if s.value != nil && s.value[0] == '"' || v != nil && v[0] == '"' {
		var right json.RawMessage
		if v == nil {
			right = raw("undefined")
		} else {
			right, _ = protocol.JavaScriptStringJSON(v)
		}
		return jsSum{value: joinRaw(s.text(), right)}
	}
	n := s.number() + jsNumber(v)
	if math.IsNaN(n) {
		return jsSum{nan: true}
	}
	return jsSum{value: number(n)}
}
func (s jsSum) plusNumber(v float64) jsSum {
	n := s.number() + v
	if math.IsNaN(n) {
		return jsSum{nan: true}
	}
	return jsSum{value: number(n)}
}
func (s jsSum) json() json.RawMessage {
	if s.nan {
		return raw(nil)
	}
	if s.value == nil {
		return raw(0)
	}
	return s.value
}
