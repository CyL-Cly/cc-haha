package jsonschemavalidator

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

// ActorFactory captures language intrinsics before an untrusted script runs.
// Every operation is called on that same language actor. No host capability,
// generated-code constructor or compiled validator is exposed to the script.
// Unlike the JSON-only bridge, this preserves live schema objects and getters.
// Ajv uses its private, captured Function solely for JSON Schema code generation;
// the workflow's public Function/eval restrictions remain in effect.
type ActorFactory struct {
	vm         *goja.Runtime
	factory    goja.Callable
	intrinsics []goja.Value
	compiler   *ActorCompiler
	proof      []actorObjectProof
	globals    map[string]goja.Value
	descriptor goja.Callable
}
type ActorCompiler struct {
	factory                  *ActorFactory
	vm                       *goja.Runtime
	build, parse, errorsText goja.Callable
	cache                    map[*goja.Object]*ActorValidator
	warnings                 []string
}
type ActorValidator struct {
	owner      *ActorCompiler
	call       goja.Callable
	function   goja.Value
	buildError string
}

var actorProgramOnce sync.Once
var actorProgram *goja.Program
var actorProgramError error
var actorIntrinsicNames = []string{"Function", "Object", "Array", "String", "Number", "Boolean", "RegExp", "Date", "Math", "JSON", "Map", "Set", "WeakMap", "WeakSet", "Symbol", "BigInt", "Error", "TypeError", "Reflect", "Proxy", "parseInt", "parseFloat", "isNaN", "isFinite"}

func NewActorFactory(vm *goja.Runtime) (*ActorFactory, error) {
	if vm == nil {
		return nil, &CapabilityError{"actual schema language actor"}
	}
	actorProgramOnce.Do(func() {
		// The bundled dependency is a generic primitive, never original CLI code.
		// Its global export is confined to a closure-local null-prototype object.
		params := append(append([]string{}, actorIntrinsicNames...), "console")
		source := "(function(" + strings.Join(params, ",") + "){const globalThis=Object.create(null);" + ajvBundle + actorBridgeSource
		actorProgram, actorProgramError = goja.Compile("ajv-8.18.0-actor.js", source, false)
	})
	if actorProgramError != nil {
		return nil, &CapabilityError{actorProgramError.Error()}
	}
	value, e := vm.RunProgram(actorProgram)
	if e != nil {
		return nil, e
	}
	callable, ok := goja.AssertFunction(value)
	if !ok {
		return nil, &CapabilityError{"generic schema factory"}
	}
	f := &ActorFactory{vm: vm, factory: callable}
	f.descriptor, _ = goja.AssertFunction(vm.Get("Object").ToObject(vm).Get("getOwnPropertyDescriptor"))
	for _, name := range actorIntrinsicNames {
		f.intrinsics = append(f.intrinsics, actorValue(vm.Get(name)))
	}
	return f, nil
}
func (f *ActorFactory) CompilerOnActor() (*ActorCompiler, error) {
	if f == nil || f.vm == nil {
		return nil, &CapabilityError{"owning schema language actor"}
	}
	if e := f.CheckOnActor(); e != nil {
		return nil, e
	}
	if f.compiler != nil {
		return f.compiler, nil
	}
	c := &ActorCompiler{vm: f.vm, factory: f, cache: map[*goja.Object]*ActorValidator{}}
	console := f.vm.NewObject()
	for _, name := range []string{"log", "warn", "error"} {
		_ = console.Set(name, func(call goja.FunctionCall) goja.Value {
			for _, v := range call.Arguments {
				c.warnings = append(c.warnings, v.String())
			}
			return goja.Undefined()
		})
	}
	args := append(append([]goja.Value{}, f.intrinsics...), console)
	value, e := f.factory(goja.Undefined(), args...)
	if e != nil {
		return nil, e
	}
	object := value.ToObject(f.vm)
	c.parse, _ = goja.AssertFunction(object.Get("parse"))
	c.build, _ = goja.AssertFunction(object.Get("build"))
	c.errorsText, _ = goja.AssertFunction(object.Get("errors"))
	if c.parse == nil || c.build == nil || c.errorsText == nil {
		return nil, &CapabilityError{"generic schema actor functions"}
	}
	f.compiler = c
	return c, nil
}

// CompileOnActor mirrors the original WeakMap cache, including failed builds.
// Live reflection preserves the original receiver; throwing getters acquire
// the original host-realm catch diagnostic without exposing that host realm.
// Functions, undefined, prototypes, and
// repeated getters are not rewritten through a JSON transport before validation.
func (c *ActorCompiler) CompileOnActor(schema *goja.Object) (*ActorValidator, error) {
	if c == nil || schema == nil {
		return nil, &CapabilityError{"actual schema object"}
	}
	if e := c.factory.CheckOnActor(); e != nil {
		return nil, e
	}
	if cached := c.cache[schema]; cached != nil {
		return cached, nil
	}
	value, e := c.build(goja.Undefined(), schema)
	if e != nil {
		return nil, e
	}
	object := value.ToObject(c.vm)
	v := &ActorValidator{owner: c}
	if message := object.Get("error"); message != nil && !goja.IsUndefined(message) {
		v.buildError = message.String()
	} else {
		var ok bool
		v.function = object.Get("validate")
		v.call, ok = goja.AssertFunction(v.function)
		if !ok {
			return nil, &CapabilityError{"compiled live schema validator"}
		}
	}
	if e := c.factory.CheckOnActor(); e != nil {
		return nil, e
	}
	c.cache[schema] = v
	return v, nil
}
func (v *ActorValidator) BuildError() string {
	if v == nil {
		return ""
	}
	return v.buildError
}
func (v *ActorValidator) ValidateOnActor(input protocol.JSONObject) error {
	if v == nil || v.owner == nil || v.call == nil {
		return &CapabilityError{"owning live schema validator"}
	}
	c := v.owner
	if e := c.factory.CheckOnActor(); e != nil {
		return e
	}
	raw, e := input.MarshalJSON()
	if e != nil {
		return e
	}
	parsed, e := c.parse(goja.Undefined(), c.vm.ToValue(string(raw)))
	if e != nil {
		return e
	}
	valid, e := v.call(goja.Undefined(), parsed)
	if e != nil {
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
}

// JSON input is rebuilt within the actor; no foreign Go object enters a schema.
func (v *ActorValidator) ValidateJSONOnActor(raw json.RawMessage) error {
	var value protocol.JSONObject
	if e := value.UnmarshalJSON(raw); e != nil {
		return e
	}
	return v.ValidateOnActor(value)
}

// The original validator runs in the host language realm, whose intrinsics a
// workflow cannot mutate. Until separate-realm reflection is ported, reject an
// actual modified intrinsic rather than silently compiling under its semantics.
// Seal follows the workflow's legitimate determinism guards, before user code.
type actorObjectProof struct {
	object      *goja.Object
	prototype   *goja.Object
	keys        []goja.Value
	descriptors [][]goja.Value
}

func (f *ActorFactory) ownKeys(o *goja.Object) []goja.Value {
	keys := []goja.Value{}
	for _, name := range o.GetOwnPropertyNames() {
		keys = append(keys, f.vm.ToValue(name))
	}
	for _, symbol := range o.Symbols() {
		keys = append(keys, symbol)
	}
	return keys
}
func (f *ActorFactory) properties(o *goja.Object, key goja.Value) ([]goja.Value, error) {
	value, e := f.descriptor(goja.Undefined(), o, key)
	if e != nil {
		return nil, e
	}
	if goja.IsUndefined(value) {
		return nil, &CapabilityError{"missing intrinsic descriptor"}
	}
	d := value.ToObject(f.vm)
	out := []goja.Value{}
	for _, field := range []string{"value", "get", "set", "writable", "enumerable", "configurable"} {
		out = append(out, actorValue(d.Get(field)))
	}
	return out, nil
}
func (f *ActorFactory) SealOnActor() error {
	f.globals = map[string]goja.Value{}
	seen := map[*goja.Object]bool{}
	add := func(o *goja.Object) error {
		if o == nil || seen[o] {
			return nil
		}
		seen[o] = true
		p := actorObjectProof{object: o, prototype: o.Prototype(), keys: f.ownKeys(o)}
		for _, key := range p.keys {
			fields, e := f.properties(o, key)
			if e != nil {
				return e
			}
			p.descriptors = append(p.descriptors, fields)
		}
		f.proof = append(f.proof, p)
		return nil
	}
	f.proof = nil
	for _, name := range actorIntrinsicNames {
		f.globals[name] = actorValue(f.vm.Get(name))
	}
	for _, value := range f.intrinsics {
		if o, ok := value.(*goja.Object); ok {
			if e := add(o); e != nil {
				return e
			}
			if p, ok := o.Get("prototype").(*goja.Object); ok {
				if e := add(p); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func (f *ActorFactory) CheckOnActor() error {
	if f == nil || f.globals == nil {
		return &CapabilityError{"sealed schema language intrinsics"}
	}
	for name, value := range f.globals {
		if !value.SameAs(actorValue(f.vm.Get(name))) {
			return &CapabilityError{"workflow-modified schema intrinsic " + name}
		}
	}
	for _, p := range f.proof {
		keys := f.ownKeys(p.object)
		if len(keys) != len(p.keys) || p.object.Prototype() != p.prototype {
			return &CapabilityError{"workflow-modified schema intrinsic properties"}
		}
		for i, key := range keys {
			if !key.SameAs(p.keys[i]) {
				return &CapabilityError{"workflow-modified schema intrinsic properties"}
			}
			fields, e := f.properties(p.object, key)
			if e != nil {
				return e
			}
			for j, value := range fields {
				if !value.SameAs(p.descriptors[i][j]) {
					return &CapabilityError{"workflow-modified schema intrinsic property " + key.String()}
				}
			}
		}
	}
	return nil
}

func actorValue(value goja.Value) goja.Value {
	if value == nil {
		return goja.Undefined()
	}
	return value
}
