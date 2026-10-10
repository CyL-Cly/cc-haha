// Package javascriptparse is a pure language boundary. Only the embedded trusted
// Acorn parser executes; caller scripts are strings passed to parse, never code.
package javascriptparse

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/dop251/goja"
	gojaparser "github.com/dop251/goja/parser"
)

//go:embed assets/acorn.js
var bundle string

const Version = "8.16.0"

type Failure struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Message  []uint16 `json:"message"`
	Pos      *Value   `json:"pos,omitempty"`
	RaisedAt *Value   `json:"raisedAt,omitempty"`
	Loc      *Value   `json:"loc,omitempty"`
	Stack    []uint16 `json:"stack,omitempty"`
	cause    error
}

func (f *Failure) Error() string { return "JavaScript language " + f.Kind + ": " + f.Name }
func (f *Failure) Unwrap() error { return f.cause }

// CapabilityError preserves the actual parsed AST while refusing its incomplete
// runtime literal domain. Callers must not accept that AST as Source-compatible.
type CapabilityError struct {
	Feature   string `json:"feature"`
	Positions []int  `json:"positions"`
}

func (e *CapabilityError) Error() string {
	return "JavaScript language capability unavailable: " + e.Feature
}

type Result struct {
	Accepted          bool             `json:"accepted"`
	AST               *Value           `json:"ast,omitempty"`
	Failure           *Failure         `json:"failure,omitempty"`
	RegExpUnavailable []int            `json:"regexpUnavailable,omitempty"`
	Capability        *CapabilityError `json:"capability,omitempty"`
}
type Parser struct {
	owner *cooperative.Owner
	vm    *goja.Runtime
	parse goja.Callable
}

func BundleSHA256() string { v := sha256.Sum256([]byte(bundle)); return hex.EncodeToString(v[:]) }

// New must run on the supplied existing owner. It does not add event loops,
// modules, filesystem/env/network bindings, worker RPC or another owner.
func New(scope *cooperative.Scope, owner *cooperative.Owner) (*Parser, error) {
	return newParser(scope, owner, bundle)
}
func newParser(scope *cooperative.Scope, owner *cooperative.Owner, trustedBundle string) (*Parser, error) {
	if owner == nil || !scope.BelongsTo(owner) {
		return nil, cooperative.ErrRestricted
	}
	var parser *Parser
	err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
		vm := goja.New()
		// Goja otherwise has a default filesystem sourcemap loader. The only
		// executable input is our fixed UMD, and no loader is available here.
		vm.SetParserOptions(gojaparser.WithDisableSourceMaps)
		if _, err := vm.RunScript("pinned-acorn-8.16.0.js", trustedBundle); err != nil {
			return failure(vm, "initialization", err)
		}
		acorn := vm.Get("acorn")
		if acorn == nil || goja.IsUndefined(acorn) || goja.IsNull(acorn) {
			return &Failure{Kind: "initialization", Name: "MissingAcorn"}
		}
		object := acorn.ToObject(vm)
		version := object.Get("version")
		if version == nil || version.String() != Version {
			return &Failure{Kind: "initialization", Name: "UnexpectedAcornVersion"}
		}
		callable, ok := goja.AssertFunction(object.Get("parse"))
		if !ok {
			return &Failure{Kind: "initialization", Name: "MissingParseFunction"}
		}
		parser = &Parser{owner: owner, vm: vm, parse: callable}
		return nil
	})
	return parser, err
}

// Parse is synchronous in the existing scope, matching Source acorn.parse. It
// returns independent immutable language data; no goja value leaves this owner.
func (p *Parser) Parse(scope *cooperative.Scope, original []uint16) (Result, error) {
	return p.parseWithOptions(scope, original, true)
}

// ParseModule matches Acorn's strict module entry point used by Computer Use
// cells. The existing tool-input parser intentionally accepts outside returns;
// those settings must not leak into the executable REPL compiler.
func (p *Parser) ParseModule(scope *cooperative.Scope, original []uint16) (Result, error) {
	return p.parseWithOptions(scope, original, false)
}

func (p *Parser) parseWithOptions(scope *cooperative.Scope, original []uint16, outside bool) (Result, error) {
	if p == nil || !scope.BelongsTo(p.owner) {
		return Result{}, cooperative.ErrRestricted
	}
	result := Result{}
	err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
		options := p.vm.NewObject()
		for _, field := range []struct {
			name  string
			value any
		}{{"ecmaVersion", "latest"}, {"sourceType", "module"}, {"allowAwaitOutsideFunction", outside}, {"allowReturnOutsideFunction", outside}} {
			if err := options.Set(field.name, field.value); err != nil {
				return failure(p.vm, "runtime", err)
			}
		}
		value, err := p.parse(goja.Undefined(), goja.StringFromUTF16(original), options)
		if err != nil {
			f := failure(p.vm, "runtime", err)
			if f.Kind == "syntax" {
				result.Failure = f
				return nil
			}
			return f
		}
		encoder := newEncoder()
		var encoded *Value
		if ex := p.vm.Try(func() { encoded = encoder.encode(value) }); ex != nil {
			return failure(p.vm, "runtime", ex)
		}
		result = Result{Accepted: true, AST: encoded, RegExpUnavailable: encoder.unavailable}
		if len(encoder.unavailable) > 0 {
			result.Capability = &CapabilityError{Feature: "RegExpConstructor", Positions: append([]int(nil), encoder.unavailable...)}
			return result.Capability
		}
		return nil
	})
	return result, err
}
func failure(vm *goja.Runtime, phase string, err error) *Failure {
	f := &Failure{Kind: phase, Name: "RuntimeError", cause: err}
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		f.Kind = "interrupted"
		f.Name = "InterruptedError"
		return f
	}
	var exception *goja.Exception
	if !errors.As(err, &exception) {
		return f
	}
	obj, ok := exception.Value().(*goja.Object)
	if !ok {
		f.Message = stringUnits(exception.Value())
		return f
	}
	if ex := vm.Try(func() {
		if name := obj.Get("name"); name != nil {
			f.Name = name.String()
		}
		f.Message = stringUnits(obj.Get("message"))
		f.Stack = stringUnits(obj.Get("stack"))
		encoder := newEncoder()
		for _, key := range obj.GetOwnPropertyNames() {
			switch key {
			case "pos":
				f.Pos = encoder.encode(obj.Get(key))
			case "raisedAt":
				f.RaisedAt = encoder.encode(obj.Get(key))
			case "loc":
				f.Loc = encoder.encode(obj.Get(key))
			}
		}
	}); ex != nil {
		return &Failure{Kind: phase, Name: "ErrorObservationFailure", cause: err}
	}
	if phase == "runtime" && f.Name == "SyntaxError" && f.Pos != nil && f.Loc != nil && f.RaisedAt != nil {
		f.Kind = "syntax"
	}
	return f
}
