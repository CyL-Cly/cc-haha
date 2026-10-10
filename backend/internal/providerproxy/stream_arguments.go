package providerproxy

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type responseDeepValue struct {
	kind   byte
	text   string
	number float64
	array  []responseDeepValue
	object map[string]responseDeepValue
}

func parseResponseDeep(raw json.RawMessage) (responseDeepValue, bool) {
	if !json.Valid(raw) {
		return responseDeepValue{}, false
	}
	raw = bytes.TrimSpace(raw)
	switch raw[0] {
	case '"':
		return responseDeepValue{kind: 's', text: responseStringKey(raw)}, true
	case '{':
		value := responseObject(raw)
		out := map[string]responseDeepValue{}
		for _, field := range value.OwnProperties() {
			child, valid := parseResponseDeep(field.Value)
			if !valid {
				return responseDeepValue{}, false
			}
			out[responseStringKey(field.NameJSON)] = child
		}
		return responseDeepValue{kind: 'o', object: out}, true
	case '[':
		var rows []json.RawMessage
		_ = json.Unmarshal(raw, &rows)
		out := []responseDeepValue{}
		for _, row := range rows {
			child, valid := parseResponseDeep(row)
			if !valid {
				return responseDeepValue{}, false
			}
			out = append(out, child)
		}
		return responseDeepValue{kind: 'a', array: out}, true
	case 'n':
		return responseDeepValue{kind: 'n'}, true
	case 't', 'f':
		return responseDeepValue{kind: 'b', text: string(raw)}, true
	default:
		n, _ := strconv.ParseFloat(string(raw), 64)
		return responseDeepValue{kind: 'd', number: n}, true
	}
}
func equalResponseDeep(a, b responseDeepValue) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case 'd':
		return a.number == b.number && (a.number != 0 || math.Signbit(a.number) == math.Signbit(b.number))
	case 's', 'b':
		return a.text == b.text
	case 'a':
		if len(a.array) != len(b.array) {
			return false
		}
		for i := range a.array {
			if !equalResponseDeep(a.array[i], b.array[i]) {
				return false
			}
		}
		return true
	case 'o':
		if len(a.object) != len(b.object) {
			return false
		}
		for key, child := range a.object {
			other, ok := b.object[key]
			if !ok || !equalResponseDeep(child, other) {
				return false
			}
		}
		return true
	}
	return true
}
func responseArgumentsEqual(a, b []uint16) bool {
	left, valid := parseResponseDeep([]byte(protocol.JavaScriptJSONSourceUTF16(a)))
	if !valid || left.kind != 'o' {
		return false
	}
	right, valid := parseResponseDeep([]byte(protocol.JavaScriptJSONSourceUTF16(b)))
	return valid && right.kind == 'o' && equalResponseDeep(left, right)
}
