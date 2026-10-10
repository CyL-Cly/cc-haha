package javascriptparse

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

func text(s string) []uint16 { return utf16.Encode([]rune(s)) }
func runParser(t *testing.T, cb func(*cooperative.Scope, *Parser) error) {
	t.Helper()
	owner := cooperative.New()
	defer owner.Close()
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		p, err := New(scope, owner)
		if err != nil {
			return err
		}
		return cb(scope, p)
	}); err != nil {
		t.Fatal(err)
	}
}
func withoutStack(v Result) Result {
	if v.Failure != nil {
		copy := *v.Failure
		copy.Stack = nil
		v.Failure = &copy
	}
	return v
}

func TestStrictModuleREPLDoesNotInheritInputParserReturnAllowance(t *testing.T) {
	runParser(t, func(scope *cooperative.Scope, p *Parser) error {
		permissive, err := p.Parse(scope, text("return 42"))
		if err != nil || !permissive.Accepted {
			t.Fatalf("input parser changed: %+v %v", permissive, err)
		}
		strict, err := p.ParseModule(scope, text("return 42"))
		if err != nil || strict.Accepted || strict.Failure == nil || strict.Failure.Name != "SyntaxError" {
			t.Fatalf("module accepted outside return: %+v %v", strict, err)
		}
		await, err := p.ParseModule(scope, text("const result = await Promise.resolve(42)"))
		if err != nil || !await.Accepted {
			t.Fatalf("module rejected top-level await: %+v %v", await, err)
		}
		return nil
	})
}
func TestOriginalSourceContracts(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var profiles []struct {
		Original struct {
			ID     string          `json:"id"`
			Script json.RawMessage `json:"script"`
		} `json:"original"`
		Expected   json.RawMessage `json:"sourceExpected"`
		Capability bool            `json:"constructorCapability"`
	}
	if err = json.Unmarshal(raw, &profiles); err != nil {
		t.Fatal(err)
	}
	runParser(t, func(scope *cooperative.Scope, p *Parser) error {
		for _, f := range profiles {
			t.Run(f.Original.ID, func(t *testing.T) {
				units, err := protocol.DecodeJavaScriptJSONString(f.Original.Script)
				if err != nil {
					t.Fatal(err)
				}
				result, err := p.Parse(scope, units)
				if f.Capability {
					var cap *CapabilityError
					if !errors.As(err, &cap) || cap.Feature != "RegExpConstructor" || !result.Accepted || result.Capability != cap || len(result.RegExpUnavailable) != 1 {
						t.Fatalf("actual constructor gap not preserved: %v %+v", err, result)
					}
					expected := Result{}
					if e := json.Unmarshal(f.Expected, &expected); e != nil {
						t.Fatal(e)
					}
					a, _ := json.Marshal(result.AST)
					b, _ := json.Marshal(expected.AST)
					if string(a) == string(b) {
						t.Fatal("runtime-null must not masquerade as Source RegExp")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(withoutStack(result))
				if err != nil {
					t.Fatal(err)
				}
				var a, b any
				_ = json.Unmarshal(encoded, &a)
				_ = json.Unmarshal(f.Expected, &b)
				actual, _ := json.Marshal(a)
				expected, _ := json.Marshal(b)
				if string(actual) != string(expected) {
					t.Fatalf("strict original language contract mismatch: %s", f.Original.ID)
				}
			})
		}
		return nil
	})
}
func TestOwnerAdmissionAndIsolation(t *testing.T) {
	if _, err := New(nil, nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	if _, err := (*Parser)(nil).Parse(nil, nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	owner := cooperative.New()
	other := cooperative.New()
	defer owner.Close()
	defer other.Close()
	var p *Parser
	var retained *cooperative.Scope
	err := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		retained = s
		var err error
		p, err = New(s, owner)
		if err != nil {
			return err
		}
		for _, name := range []string{"process", "require", "fetch", "Buffer", "__ACORN_USER_BODY_EXECUTED"} {
			if v := p.vm.Get(name); v != nil && !goja.IsUndefined(v) {
				t.Fatalf("ambient binding %s", name)
			}
		}
		_, err = p.Parse(s, text("globalThis.__ACORN_USER_BODY_EXECUTED=true; while(true){}"))
		if err != nil {
			return err
		}
		if v := p.vm.Get("__ACORN_USER_BODY_EXECUTED"); v != nil && !goja.IsUndefined(v) {
			t.Fatal("workflow body executed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Parse(retained, text("const x=1")); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("expired scope admitted", err)
	}
	if err = other.Run(context.Background(), func(s *cooperative.Scope) error {
		_, err := p.Parse(s, nil)
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		_, err = New(s, owner)
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = owner.Run(ctx, func(s *cooperative.Scope) error { _, err := p.Parse(s, nil); return err }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(BundleSHA256()) != 64 || Version != "8.16.0" {
		t.Fatal("unbound language asset")
	}
}
func TestInitializationAndFailureOwnership(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	cases := []struct{ code, name string }{{"throw new Error('init')", "Error"}, {"", "MissingAcorn"}, {"var acorn={version:'other'}", "UnexpectedAcornVersion"}, {"var acorn={version:'8.16.0',parse:1}", "MissingParseFunction"}}
	if err := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		for _, c := range cases {
			_, err := newParser(s, owner, c.code)
			var f *Failure
			if !errors.As(err, &f) || f.Kind != "initialization" || f.Name != c.name || !strings.Contains(f.Error(), c.name) {
				t.Fatalf("%s: %+v", c.code, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runParser(t, func(s *cooperative.Scope, p *Parser) error {
		result, err := p.Parse(s, text("const x='\\xZZ'"))
		if err != nil || result.Accepted || result.Failure == nil || result.Failure.Kind != "syntax" || result.Failure.Pos == nil || result.Failure.RaisedAt == nil || result.Failure.Loc == nil || len(result.Failure.Stack) == 0 {
			t.Fatalf("original Acorn syntax not preserved: %v %+v", err, result)
		}
		cause := errors.New("language execution interrupted")
		p.vm.Interrupt(cause)
		_, err = p.Parse(s, text("const x=1"))
		var failure *Failure
		if !errors.As(err, &failure) || failure.Kind != "interrupted" || !errors.Is(err, cause) {
			t.Fatalf("interrupt ownership: %v", err)
		}
		p.vm.ClearInterrupt()
		if _, err = p.Parse(s, text("return 1")); err != nil {
			return err
		}
		return nil
	})
}
func TestRuntimeFaultIsNotAcornSyntax(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	if err := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		for _, body := range []string{"throw new TypeError('language host fault')", "throw 'scalar'", "throw null", "throw {name:'SyntaxError',message:'not Acorn'}", "throw {get name(){throw new Error('observer')}}"} {
			p, err := newParser(s, owner, "var acorn={version:'8.16.0',parse:function(){"+body+"}};")
			if err != nil {
				return err
			}
			_, err = p.Parse(s, nil)
			var f *Failure
			if !errors.As(err, &f) || f.Kind != "runtime" || f.Unwrap() == nil {
				t.Fatalf("runtime was misclassified: %+v", err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
