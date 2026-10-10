package workflow

import "github.com/dop251/goja"

type scriptError struct{ Name, Message string }

func (e *scriptError) Error() string { return e.Message }
func (x *execution) errorValue(err error) goja.Value {
	if exception, ok := err.(*goja.Exception); ok {
		return exception.Value()
	}
	value, e := x.errorConstructor(goja.Undefined(), x.vm.ToValue(err.Error()))
	if e != nil {
		return x.vm.NewGoError(err)
	}
	if named, ok := err.(*scriptError); ok {
		_ = value.ToObject(x.vm).Set("name", named.Name)
	}
	return value
}
