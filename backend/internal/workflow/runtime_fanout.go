package workflow

import (
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

func (x *execution) rejected(err error) goja.Value {
	promise, _, reject := x.vm.NewPromise()
	var value goja.Value = x.errorValue(err)
	if exception, ok := err.(*goja.Exception); ok {
		value = exception.Value()
	}
	_ = reject(value)
	return x.vm.ToValue(promise)
}
func (x *execution) array(value goja.Value, message string) ([]goja.Value, error) {
	check, e := x.arrayCheck(goja.Undefined(), value)
	if e != nil {
		return nil, e
	}
	if !check.ToBoolean() {
		return nil, &scriptError{Name: "TypeError", Message: message}
	}
	object := value.ToObject(x.vm)
	length := int(object.Get("length").ToInteger())
	if length > 4096 {
		return nil, &scriptError{Name: "RangeError", Message: fmt.Sprintf("A single parallel()/pipeline() call accepts at most 4096 items (got %d)", length)}
	}
	copy, e := x.cloneArray(goja.Undefined(), value)
	if e != nil {
		return nil, e
	}
	out := make([]goja.Value, length)
	for i := range out {
		out[i] = copy.ToObject(x.vm).Get(fmt.Sprint(i))
	}
	return out, nil
}
func (x *execution) collect(values []goja.Value, kind string) goja.Value {
	array := x.vm.NewArray()
	for i, value := range values {
		_ = array.Set(fmt.Sprint(i), value)
	}
	settled, e := x.allSettled(x.vm.Get("Promise"), array)
	if e != nil {
		return x.rejected(e)
	}
	promise, resolve, reject := x.vm.NewPromise()
	then, _ := goja.AssertFunction(settled.ToObject(x.vm).Get("then"))
	_, e = then(settled, x.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		records := call.Argument(0).ToObject(x.vm)
		length := int(records.Get("length").ToInteger())
		out := x.vm.NewArray()
		budgetDrops := 0
		for i := 0; i < length; i++ {
			record := records.Get(fmt.Sprint(i)).ToObject(x.vm)
			value := record.Get("value")
			if record.Get("status").String() == "rejected" {
				value = goja.Null()
				reason := record.Get("reason")
				name := ""
				if object, ok := reason.(*goja.Object); ok {
					_ = x.vm.Try(func() {
						if v := object.Get("name"); v != nil && !goja.IsNull(v) && !goja.IsUndefined(v) {
							name = v.String()
						}
					})
				}
				if name == "WorkflowBudgetExceededError" {
					budgetDrops++
					_ = out.Set(fmt.Sprint(i), value)
					continue
				}
				message := fmt.Sprintf("%s[%d] failed: %s", kind, i, x.describeValue(record.Get("reason")))
				x.result.Failures = append(x.result.Failures, message)
				if e := x.emit(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("workflow_log")}, protocol.JSONField{Name: "message", Value: quoted(message)})); e != nil {
					_ = reject(x.vm.NewGoError(e))
					return goja.Undefined()
				}
			}
			_ = out.Set(fmt.Sprint(i), value)
		}
		if budgetDrops > 0 {
			suffix := "s"
			if budgetDrops == 1 {
				suffix = ""
			}
			x.result.Failures = append(x.result.Failures, fmt.Sprintf("%s: %d slot%s dropped — token budget exceeded", kind, budgetDrops, suffix))
		}
		_ = resolve(out)
		return goja.Undefined()
	}), x.vm.ToValue(func(call goja.FunctionCall) goja.Value { _ = reject(call.Argument(0)); return goja.Undefined() }))
	if e != nil {
		return x.rejected(e)
	}
	return x.vm.ToValue(promise)
}
func (x *execution) parallel(call goja.FunctionCall) goja.Value {
	values, e := x.array(call.Argument(0), "parallel() expects an array of functions")
	if e != nil {
		return x.rejected(e)
	}
	if len(values) == 0 {
		return x.collect(nil, "parallel")
	}
	if e := x.assertLimits(); e != nil {
		return x.rejected(e)
	}
	functions := []goja.Callable{}
	for _, value := range values {
		fn, ok := goja.AssertFunction(value)
		if !ok {
			return x.rejected(&scriptError{Name: "TypeError", Message: "parallel() expects an array of functions, not promises. Wrap each call: () => agent(...)"})
		}
		functions = append(functions, fn)
	}
	results := []goja.Value{}
	for _, function := range functions {
		value, e := function(goja.Undefined())
		if e != nil {
			value = x.rejected(e)
		}
		results = append(results, value)
	}
	return x.collect(results, "parallel")
}
func (x *execution) pipeline(call goja.FunctionCall) goja.Value {
	items, e := x.array(call.Argument(0), "pipeline() expects an array as the first argument")
	if e != nil {
		return x.rejected(e)
	}
	if len(items) == 0 {
		return x.collect(nil, "pipeline")
	}
	if e := x.assertLimits(); e != nil {
		return x.rejected(e)
	}
	stages := []goja.Callable{}
	for _, argument := range call.Arguments[1:] {
		check, e := x.arrayCheck(goja.Undefined(), argument)
		if e != nil {
			return x.rejected(e)
		}
		values := []goja.Value{argument}
		if check.ToBoolean() {
			values, e = x.array(argument, "pipeline stages")
			if e != nil {
				return x.rejected(e)
			}
		}
		for _, value := range values {
			fn, ok := goja.AssertFunction(value)
			if !ok {
				return x.rejected(&scriptError{Name: "TypeError", Message: "pipeline() stages must be functions: pipeline(items, item => ..., result => ...)"})
			}
			stages = append(stages, fn)
		}
	}
	results := []goja.Value{}
	for i, item := range items {
		promise, resolve, reject := x.vm.NewPromise()
		var next func(int, goja.Value)
		next = func(index int, value goja.Value) {
			if index == len(stages) || goja.IsNull(value) {
				_ = resolve(value)
				return
			}
			result, e := stages[index](goja.Undefined(), value, item, x.vm.ToValue(i))
			if e != nil {
				exception, ok := e.(*goja.Exception)
				if ok {
					_ = reject(exception.Value())
				} else {
					_ = reject(x.vm.NewGoError(e))
				}
				return
			}
			fulfilled, resolver, _ := x.vm.NewPromise()
			_ = resolver(result)
			then, _ := goja.AssertFunction(x.vm.ToValue(fulfilled).ToObject(x.vm).Get("then"))
			_, e = then(x.vm.ToValue(fulfilled), x.vm.ToValue(func(call goja.FunctionCall) goja.Value { next(index+1, call.Argument(0)); return goja.Undefined() }), x.vm.ToValue(func(call goja.FunctionCall) goja.Value { _ = reject(call.Argument(0)); return goja.Undefined() }))
			if e != nil {
				_ = reject(x.vm.NewGoError(e))
			}
		}
		next(0, item)
		results = append(results, x.vm.ToValue(promise))
	}
	return x.collect(results, "pipeline")
}
