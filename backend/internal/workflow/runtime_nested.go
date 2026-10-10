package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

func (x *execution) nativeNested(call goja.FunctionCall) goja.Value {
	promise, resolve, reject := x.vm.NewPromise()
	fail := func(e error) goja.Value { _ = reject(x.errorValue(e)); return x.vm.ToValue(promise) }
	if x.options.PrepareNested == nil {
		return fail(errors.New("workflow() is not available in this run"))
	}
	reference := call.Argument(0)
	var ref json.RawMessage
	if _, ok := reference.(goja.String); ok {
		var e error
		ref, e = x.export(reference, false)
		if e != nil {
			return fail(e)
		}
	} else if object, ok := reference.(*goja.Object); ok {
		path := goja.Undefined()
		if exception := x.vm.Try(func() { path = object.Get("scriptPath") }); exception != nil {
			_ = reject(exception.Value())
			return x.vm.ToValue(promise)
		}
		if _, ok := path.(goja.String); !ok {
			return fail(errors.New("workflow() expects a saved workflow name or { scriptPath: '...' }"))
		}
		raw, e := x.export(path, false)
		if e != nil {
			return fail(e)
		}
		ref, e = protocol.NewJSONObject(protocol.JSONField{Name: "scriptPath", Value: raw}).MarshalJSON()
		if e != nil {
			return fail(e)
		}
	} else {
		return fail(errors.New("workflow() expects a saved workflow name or { scriptPath: '...' }"))
	}
	// The original runner retains args through async file discovery and only
	// rebuilds them when its child realm starts. Keep this real value on the
	// language actor; no worker dereferences it before the admitted reaction.
	argsValue := call.Argument(1)
	x.jobs.Add(1)
	go func() {
		defer x.jobs.Done()
		prepared, cause := x.options.PrepareNested(x.ctx, ref)
		var outcome ExecutionOutcome
		if cause == nil {
			name := prepared.Meta.String("name")
			cause = x.options.Emit(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("workflow_log")}, protocol.JSONField{Name: "message", Value: quoted("workflow(" + name + ") started")}))
			if cause == nil {
				var args json.RawMessage
				cause = x.post(x.ctx, func() error {
					if goja.IsUndefined(argsValue) {
						return nil
					}
					raw, e := x.argsStringify(goja.Undefined(), argsValue)
					if e != nil {
						var exception *goja.Exception
						if errors.As(e, &exception) {
							object := exception.Value().ToObject(x.vm)
							message := object.Get("message").String()
							if object.Get("name").String() == "TypeError" {
								switch message {
								case "Converting circular structure to JSON":
									return &scriptError{Name: "TypeError", Message: "JSON.stringify cannot serialize cyclic structures."}
								case "Do not know how to serialize a BigInt":
									return &scriptError{Name: "TypeError", Message: "JSON.stringify cannot serialize BigInt."}
								}
							}
							return &scriptError{Name: object.Get("name").String(), Message: message}
						}
						return e
					}
					if goja.IsUndefined(raw) {
						return nil
					}
					args, e = protocol.ParseAndStringifyJavaScriptJSON([]byte(raw.String()))
					return e
				})
				if cause != nil {
					reaction := runtimeReaction{run: func() error { return reject(x.errorValue(cause)) }}
					select {
					case x.reactions <- reaction:
					case <-x.ctx.Done():
					}
					return
				}
				var phases []protocol.JSONObject
				_ = json.Unmarshal(prepared.Meta.Get("phases"), &phases)
				titles := []string{}
				for _, phase := range phases {
					titles = append(titles, phase.String("title"))
				}
				child := x.options
				child.Program = prepared.Program
				child.WorkflowName = ""
				child.Args = args
				child.SeedPhaseTitles = titles
				child.JournalSnapshot = nil
				child.PrepareNested = nil
				child.shared = x.options.shared
				child.nested = true
				outcome, cause = Execute(x.ctx, child)
				if cause == nil && outcome.Error != "" {
					cause = fmt.Errorf("workflow(%s): %s", name, outcome.Error)
				}
			}
		}
		reaction := runtimeReaction{run: func() error {
			if cause != nil {
				return reject(x.errorValue(cause))
			}
			value, e := x.parse(goja.Undefined(), x.vm.ToValue(string(defaultRaw(outcome.Result, []byte("null")))))
			if e != nil {
				return reject(x.errorValue(e))
			}
			return resolve(value)
		}}
		select {
		case x.reactions <- reaction:
		case <-x.ctx.Done():
		}
	}()
	return x.vm.ToValue(promise)
}

func (x *execution) wakeAdmission(start func()) {
	var once sync.Once
	started := make(chan struct{})
	canceled := func() { once.Do(func() { close(started); x.options.shared.release(x) }) }
	run := func() {
		once.Do(func() {
			close(started)
			if x.ctx.Err() != nil {
				x.options.shared.release(x)
			} else {
				start()
			}
		})
	}
	go func() {
		select {
		case <-started:
		case <-x.ctx.Done():
			canceled()
		}
	}()
	reaction := runtimeReaction{run: func() error { run(); return nil }}
	select {
	case x.reactions <- reaction:
	case <-x.ctx.Done():
		canceled()
	}
}
