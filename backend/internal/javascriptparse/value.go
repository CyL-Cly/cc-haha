package javascriptparse

import (
	"math/big"
	"strconv"
	"unicode/utf16"

	"github.com/dop251/goja"
)

// Value distinguishes missing properties (absent from Properties), undefined,
// null and all language primitives. It never uses whole-AST JSON.stringify.
type Value struct {
	Kind       string     `json:"kind"`
	Text       string     `json:"text,omitempty"`
	Units      []uint16   `json:"units,omitempty"`
	Bool       *bool      `json:"bool,omitempty"`
	ID         int        `json:"id,omitempty"`
	Class      string     `json:"class,omitempty"`
	Properties []Property `json:"properties,omitempty"`
	Pattern    []uint16   `json:"pattern,omitempty"`
	Flags      []uint16   `json:"flags,omitempty"`
}
type Property struct {
	Key   []uint16 `json:"key"`
	Value *Value   `json:"value"`
}
type encoder struct {
	objects     map[*goja.Object]int
	unavailable []int
}

func newEncoder() *encoder { return &encoder{objects: map[*goja.Object]int{}} }
func stringUnits(value goja.Value) []uint16 {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	s, ok := value.(goja.String)
	if !ok {
		converted := value.ToString()
		if typed, ok := converted.(goja.String); ok {
			s = typed
		} else {
			// Goja keeps ASCII numeric/boolean ToString primitives internally.
			return utf16.Encode([]rune(converted.String()))
		}
	}
	units := make([]uint16, s.Length())
	for i := range units {
		units[i] = s.CharAt(i)
	}
	return units
}
func (e *encoder) encode(value goja.Value) *Value {
	if value == nil || goja.IsUndefined(value) {
		return &Value{Kind: "undefined"}
	}
	if goja.IsNull(value) {
		return &Value{Kind: "null"}
	}
	if s, ok := value.(goja.String); ok {
		return &Value{Kind: "string", Units: stringUnits(s)}
	}
	if obj, ok := value.(*goja.Object); ok {
		if id, ok := e.objects[obj]; ok {
			return &Value{Kind: "reference", ID: id}
		}
		id := len(e.objects) + 1
		e.objects[obj] = id
		v := &Value{Kind: "object", ID: id, Class: obj.ClassName()}
		if v.Class == "Array" {
			v.Kind = "array"
		}
		if v.Class == "RegExp" {
			v.Kind = "regexp"
			v.Pattern = stringUnits(obj.Get("source"))
			v.Flags = stringUnits(obj.Get("flags"))
		}
		for _, key := range obj.GetOwnPropertyNames() {
			v.Properties = append(v.Properties, Property{utf16.Encode([]rune(key)), e.encode(obj.Get(key))})
		}
		// Acorn grammar validation may succeed while the host RegExp constructor is
		// unavailable. Record the actual null, never fabricate a regexp value.
		if token := obj.Get("regex"); token != nil && !goja.IsUndefined(token) && !goja.IsNull(token) {
			actual := obj.Get("value")
			if goja.IsNull(actual) {
				start := obj.Get("start")
				if start != nil {
					e.unavailable = append(e.unavailable, int(start.ToInteger()))
				}
			}
		}
		return v
	}
	switch exported := value.Export().(type) {
	case bool:
		return &Value{Kind: "boolean", Bool: &exported}
	case *big.Int:
		return &Value{Kind: "bigint", Text: exported.String()}
	case int64:
		return &Value{Kind: "number", Text: strconv.FormatInt(exported, 10)}
	case float64:
		text := value.String()
		if exported == 0 && 1/exported < 0 {
			text = "-0"
		}
		return &Value{Kind: "number", Text: text}
	default:
		return &Value{Kind: "unsupported", Text: value.String()}
	}
}
