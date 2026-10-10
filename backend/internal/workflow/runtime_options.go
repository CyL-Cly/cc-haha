package workflow

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
	"math"
)

func (x *execution) captureOptions(value goja.Value) (protocol.JSONObject, error) {
	result := protocol.NewJSONObject()
	if goja.IsNull(value) || goja.IsUndefined(value) {
		return result, nil
	}
	object, ok := value.(*goja.Object)
	if !ok {
		return result, nil
	}
	read := func(key string) goja.Value {
		value := goja.Undefined()
		if exception := x.vm.Try(func() { value = object.Get(key) }); exception != nil {
			return goja.Undefined()
		}
		if value == nil {
			return goja.Undefined()
		}
		return value
	}
	for _, key := range []string{"label", "phase", "model", "effort", "agentType"} {
		value := read(key)
		if goja.IsUndefined(value) || goja.IsNull(value) {
			continue
		}
		text, e := x.stringConstructor(goja.Undefined(), value)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		result.Set(key, quoted(text.String()))
	}
	if value := read("isolation"); value.Export() == "worktree" || value.Export() == "remote" {
		result.Set("isolation", quoted(value.String()))
	}
	if value := read("stallMs"); value != nil {
		switch v := value.Export().(type) {
		case int64:
			result.Set("stallMs", valueRaw(v))
		case float64:
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				result.Set("stallMs", valueRaw(v))
			}
		}
	}
	return result, nil
}
