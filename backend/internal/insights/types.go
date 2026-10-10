package insights

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Log struct {
	SessionID, ProjectPath, FirstPrompt string
	Summary                             json.RawMessage
	CreatedMillis, ModifiedMillis       float64
	Messages                            []protocol.JSONObject
}

func raw(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func number(v float64) json.RawMessage {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return raw(nil)
	}
	return raw(v)
}
func numeric(v json.RawMessage) float64 {
	var n float64
	if json.Unmarshal(v, &n) == nil {
		return n
	}
	return math.NaN()
}
func truthy(v json.RawMessage) bool {
	v = json.RawMessage(strings.TrimSpace(string(v)))
	if len(v) == 0 || string(v) == "null" || string(v) == "false" || string(v) == `""` {
		return false
	}
	if v[0] == '"' || v[0] == '{' || v[0] == '[' || string(v) == "true" {
		return true
	}
	return numeric(v) != 0 && !math.IsNaN(numeric(v))
}
func jsNumber(v json.RawMessage) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	if string(v) == "null" || string(v) == "false" {
		return 0
	}
	if string(v) == "true" {
		return 1
	}
	if len(v) > 0 && v[0] == '"' {
		var s string
		_ = json.Unmarshal(v, &s)
		s = protocol.TrimJavaScriptString(s)
		if s == "" {
			return 0
		}
		n, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return n
		}
		return math.NaN()
	}
	return numeric(v)
}
func add(a, b json.RawMessage) json.RawMessage {
	if !truthy(a) {
		a = raw(0)
	}
	if len(a) > 0 && a[0] == '"' || len(b) > 0 && b[0] == '"' {
		av, _ := protocol.JavaScriptStringJSON(a)
		bv, _ := protocol.JavaScriptStringJSON(b)
		units, _ := protocol.DecodeJavaScriptJSONString(av)
		other, _ := protocol.DecodeJavaScriptJSONString(bv)
		return protocol.QuoteJavaScriptUTF16(append(units, other...))
	}
	return number(jsNumber(a) + jsNumber(b))
}
func object(v json.RawMessage) (protocol.JSONObject, error) {
	var o protocol.JSONObject
	if o.UnmarshalJSON(v) != nil || !o.IsObject() {
		return o, errors.New("Insights expected an object")
	}
	return o, nil
}
func sliceString(v json.RawMessage, limit int) json.RawMessage {
	units, err := protocol.DecodeJavaScriptJSONString(v)
	if err != nil {
		return raw("")
	}
	if len(units) > limit {
		units = units[:limit]
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func compare(a, b string) int {
	au, bu := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(au) && i < len(bu); i++ {
		if au[i] < bu[i] {
			return -1
		}
		if au[i] > bu[i] {
			return 1
		}
	}
	if len(au) < len(bu) {
		return -1
	}
	if len(au) > len(bu) {
		return 1
	}
	return 0
}

// ValidFacets deliberately retains Source's shallow object check: arrays are
// objects too, and optional or future fields are neither removed nor invented.
func ValidFacets(v json.RawMessage) bool {
	o, err := object(v)
	if err != nil {
		return false
	}
	for _, key := range []string{"underlying_goal", "outcome", "brief_summary"} {
		value := o.Get(key)
		if len(value) == 0 || value[0] != '"' {
			return false
		}
	}
	for _, key := range []string{"goal_categories", "user_satisfaction_counts", "friction_counts"} {
		value := o.Get(key)
		if len(value) == 0 || value[0] != '{' && value[0] != '[' {
			return false
		}
	}
	return true
}

func Deduplicate(metas []protocol.JSONObject) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	indices := map[string]int{}
	for _, meta := range metas {
		key := meta.String("session_id")
		index, exists := indices[key]
		if !exists {
			indices[key] = len(out)
			out = append(out, meta)
			continue
		}
		old := out[index]
		count, previous := jsNumber(meta.Get("user_message_count")), jsNumber(old.Get("user_message_count"))
		if count > previous || count == previous && jsNumber(meta.Get("duration_minutes")) > jsNumber(old.Get("duration_minutes")) {
			out[index] = meta
		}
	}
	return out
}
