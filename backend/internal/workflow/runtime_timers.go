package workflow

import (
	"github.com/dop251/goja"
	"math"
	"time"
)

func (x *execution) setTimer(call goja.FunctionCall) goja.Value {
	callback, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		return x.vm.ToValue(0)
	}
	delay := 0.0
	switch v := call.Argument(1).Export().(type) {
	case int64:
		delay = float64(v)
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			delay = v
		}
	}
	if delay < 1 || delay > 2147483647 {
		delay = 1
	}
	delay = math.Floor(delay)
	x.timerNext++
	id := x.timerNext
	handle := x.vm.NewObject()
	x.timerHandles[handle] = id
	timer := time.AfterFunc(time.Duration(delay)*time.Millisecond, func() {
		reaction := runtimeReaction{run: func() error {
			if x.timers[id] == nil {
				return nil
			}
			delete(x.timers, id)
			_, _ = callback(goja.Undefined())
			return nil
		}}
		select {
		case x.reactions <- reaction:
		case <-x.ctx.Done():
		}
	})
	x.timers[id] = timer
	return handle
}
func (x *execution) clearTimer(call goja.FunctionCall) goja.Value {
	object, ok := call.Argument(0).(*goja.Object)
	if !ok {
		return goja.Undefined()
	}
	id, exists := x.timerHandles[object]
	if exists {
		if timer := x.timers[id]; timer != nil {
			timer.Stop()
			delete(x.timers, id)
		}
		delete(x.timerHandles, object)
	}
	return goja.Undefined()
}
