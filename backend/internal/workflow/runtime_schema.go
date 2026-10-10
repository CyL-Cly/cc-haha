package workflow

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/jsonschemavalidator"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

// SchemaReference retains the actual script object. Its validator is compiled
// through the generic Ajv primitive on the same language actor, preserving
// functions, undefined, live getters and the original identity cache.
type SchemaReference struct {
	execution *execution
	object    *goja.Object
}
type SchemaValidator struct {
	execution *execution
	validator *jsonschemavalidator.ActorValidator
}

func (s *SchemaReference) Prepare(ctx context.Context) (*SchemaValidator, json.RawMessage, error) {
	if s == nil || s.execution == nil {
		return nil, nil, &CapabilityError{"actual schema language identity"}
	}
	x := s.execution
	var raw json.RawMessage
	var validator *jsonschemavalidator.ActorValidator
	e := x.post(ctx, func() error {
		return x.schemaWork(ctx, func() error {
			array, e := x.arrayCheck(goja.Undefined(), s.object)
			if e != nil {
				return e
			}
			if array.ToBoolean() {
				return errors.New("schema must be a JSON Schema object")
			}
			compiler, e := x.schemaFactory.CompilerOnActor()
			if e != nil {
				return e
			}
			validator, e = compiler.CompileOnActor(s.object)
			if e != nil {
				return e
			}
			if message := validator.BuildError(); message != "" {
				return errors.New(message)
			}
			// Tool advertisement is a separate stage after validation. In particular a
			// toJSON method does not replace the object Ajv has just validated.
			serialized, e := x.stringify(goja.Undefined(), s.object)
			if e != nil {
				return &CapabilityError{"live schema advertisement serializer: " + x.describeError(e)}
			}
			if goja.IsUndefined(serialized) {
				return &CapabilityError{"undefined live schema advertisement serializer"}
			}
			raw, e = protocol.ParseAndStringifyJavaScriptJSON([]byte(serialized.String()))
			return e
		})
	})
	if e != nil {
		return nil, nil, e
	}
	return &SchemaValidator{x, validator}, raw, nil
}
func (v *SchemaValidator) Validate(ctx context.Context, input protocol.JSONObject) error {
	if v == nil || v.execution == nil || v.validator == nil {
		return &CapabilityError{"actual schema actor validator"}
	}
	copy := input.Clone()
	return v.execution.post(ctx, func() error {
		return v.execution.schemaWork(ctx, func() error {
			e := v.validator.ValidateOnActor(copy)
			var invalid *jsonschemavalidator.ValidationError
			if errors.As(e, &invalid) {
				return &scriptError{Name: "TelemetrySafeError", Message: e.Error()}
			}
			return e
		})
	})
}
func (x *execution) schemaWork(ctx context.Context, run func() error) error {
	if e := ctx.Err(); e != nil {
		return context.Cause(ctx)
	}
	stopped, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-ctx.Done():
			x.vm.Interrupt(context.Cause(ctx))
		case <-x.ctx.Done():
			x.vm.Interrupt(context.Cause(x.ctx))
		case <-stopped:
		}
	}()
	e := run()
	close(stopped)
	<-finished
	x.vm.ClearInterrupt()
	return e
}
func (x *execution) captureSchema(opts goja.Value) (*SchemaReference, error) {
	object, ok := opts.(*goja.Object)
	if !ok {
		return nil, nil
	}
	value := goja.Undefined()
	if x.vm.Try(func() { value = object.Get("schema") }) != nil {
		return nil, nil
	}
	schema, ok := value.(*goja.Object)
	if !ok {
		return nil, nil
	}
	return &SchemaReference{x, schema}, nil
}
func (x *execution) schemaCacheJSON(schema *SchemaReference) (json.RawMessage, error) {
	raw, e := x.safeStringify(goja.Undefined(), schema.object)
	if e != nil {
		return nil, e
	}
	return protocol.ParseAndStringifyJavaScriptJSON([]byte(raw.String()))
}
