// Package jsonschemavalidator is a pure JSON Schema language primitive. It
// embeds the same pinned Ajv dependency as Source; it exposes no filesystem,
// process, environment, module loader, timer, network, CLI or Agent business.
package jsonschemavalidator

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

//go:embed assets/ajv.bundle.js
var ajvBundle string

//go:embed assets/provenance.json
var provenance string

//go:embed assets/*.LICENSE
var licenses embed.FS

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "JSON Schema language primitive is not available: " + e.Boundary
}

type Compiler struct {
	mu                       sync.Mutex
	vm                       *goja.Runtime
	parse, build, errorsText goja.Callable
	cache                    map[uint64]*Validator
	count                    int
	warnings                 []string
}
type Validator struct {
	owner      *Compiler
	call       goja.Callable
	function   goja.Value
	buildError string
	schema     json.RawMessage
}

func New() (*Compiler, error) {
	c := &Compiler{vm: goja.New(), cache: map[uint64]*Validator{}, warnings: []string{}}
	// Ajv's log-only strict-type warnings are consumed as language output. No
	// ambient host console or executable/module objects enter this runtime.
	console := c.vm.NewObject()
	for _, name := range []string{"log", "warn", "error"} {
		_ = console.Set(name, func(call goja.FunctionCall) goja.Value {
			for _, argument := range call.Arguments {
				c.warnings = append(c.warnings, argument.String())
			}
			return goja.Undefined()
		})
	}
	_ = c.vm.Set("console", console)
	if _, e := c.vm.RunScript("ajv-8.18.0.js", ajvBundle); e != nil {
		return nil, &CapabilityError{e.Error()}
	}
	value, e := c.vm.RunScript("ajv-language-bridge.js", `(()=>{const Ajv=__WorkflowAjv,parse=JSON.parse;delete globalThis.__WorkflowAjv;return {parse:json=>parse(json),build:schema=>{try{const ajv=new Ajv({allErrors:true});if(!ajv.validateSchema(schema))return {error:ajv.errorsText(ajv.errors)};return {validate:ajv.compile(schema)}}catch(e){return {error:e instanceof Error?e.message:String(e)}}},errors:validate=>(validate.errors??[]).map(e=>(e.instancePath||'root')+': '+e.message).join(', ')}})()`)
	if e != nil {
		return nil, &CapabilityError{e.Error()}
	}
	object := value.ToObject(c.vm)
	c.parse, _ = goja.AssertFunction(object.Get("parse"))
	c.build, _ = goja.AssertFunction(object.Get("build"))
	c.errorsText, _ = goja.AssertFunction(object.Get("errors"))
	return c, nil
}
func Provenance() string { return provenance }
func (c *Compiler) withContext(ctx context.Context, run func() error) error {
	if ctx == nil {
		return &CapabilityError{"actual caller context"}
	}
	if e := ctx.Err(); e != nil {
		return context.Cause(ctx)
	}
	stopped := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-ctx.Done():
			c.vm.Interrupt(context.Cause(ctx))
		case <-stopped:
		}
	}()
	e := run()
	close(stopped)
	<-finished
	c.vm.ClearInterrupt()
	return e
}

// identity must come from the actual language object's identity, not its JSON
// contents: equal independent schemas compile independently, and mutation of a
// cached object does not compile it a second time, exactly as Source's WeakMap.
func (c *Compiler) Compile(ctx context.Context, identity uint64, schema json.RawMessage) (*Validator, error) {
	if c == nil || identity == 0 || !json.Valid(schema) {
		return nil, &CapabilityError{"real schema object identity and JSON snapshot"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached := c.cache[identity]; cached != nil {
		return cached, nil
	}
	var out *Validator
	e := c.withContext(ctx, func() error {
		parsed, e := c.parse(goja.Undefined(), c.vm.ToValue(string(schema)))
		if e != nil {
			return &CapabilityError{e.Error()}
		}
		built, e := c.build(goja.Undefined(), parsed)
		if e != nil {
			return &CapabilityError{e.Error()}
		}
		object := built.ToObject(c.vm)
		out = &Validator{owner: c, schema: append(json.RawMessage(nil), schema...)}
		if message := object.Get("error"); message != nil && !goja.IsUndefined(message) {
			out.buildError = message.String()
		} else {
			var ok bool
			out.function = object.Get("validate")
			out.call, ok = goja.AssertFunction(out.function)
			if !ok {
				return &CapabilityError{"compiled validator function"}
			}
		}
		c.count++
		c.cache[identity] = out
		return nil
	})
	return out, e
}
func (v *Validator) BuildError() string {
	if v == nil {
		return ""
	}
	return v.buildError
}
func (v *Validator) Schema() json.RawMessage {
	if v == nil {
		return nil
	}
	return append(json.RawMessage(nil), v.schema...)
}

// Validate returns the original model-facing error. The caller retains actual
// tool permission, call result, structured-output attachment and Handler rules.
func (v *Validator) Validate(ctx context.Context, input protocol.JSONObject) error {
	if v == nil || v.owner == nil || v.call == nil {
		return &CapabilityError{"actual compiled validator"}
	}
	c := v.owner
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.withContext(ctx, func() error {
		data, e := input.MarshalJSON()
		if e != nil {
			return e
		}
		parsed, e := c.parse(goja.Undefined(), c.vm.ToValue(string(data)))
		if e != nil {
			return e
		}
		valid, e := v.call(goja.Undefined(), parsed)
		if e != nil {
			var interrupted *goja.InterruptedError
			if errors.As(e, &interrupted) {
				return fmt.Errorf("JSON Schema validation interrupted: %w", e)
			}
			return e
		}
		if valid.ToBoolean() {
			return nil
		}
		text, e := c.errorsText(goja.Undefined(), v.function)
		if e != nil {
			return e
		}
		return &ValidationError{"Output does not match required schema: " + text.String()}
	})
}
func (c *Compiler) CompiledCount() int { c.mu.Lock(); defer c.mu.Unlock(); return c.count }
