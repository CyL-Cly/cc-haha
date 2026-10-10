// Package ignorepattern runs the pinned MIT ignore language library without
// filesystem, environment, network, module-loader, or application bindings.
// Native Server code owns traversal, cancellation, configuration, and I/O.
package ignorepattern

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/dop251/goja"
	gojaparser "github.com/dop251/goja/parser"
)

const Version = "7.0.5"

//go:embed assets/ignore-7.0.5.js
var library string

var compileOnce sync.Once
var compiled *goja.Program
var compileError error

func BundleSHA256() string {
	value := sha256.Sum256([]byte(library))
	return hex.EncodeToString(value[:])
}

type Result struct{ Ignored, Unignored bool }

type Matcher struct {
	mu        sync.Mutex
	vm        *goja.Runtime
	object    goja.Value
	add, test goja.Callable
}

func New(platform string) (*Matcher, error) {
	compileOnce.Do(func() {
		parsed, err := gojaparser.ParseFile(nil, "ignore-7.0.5.js", library, 0, gojaparser.WithDisableSourceMaps)
		if err != nil {
			compileError = err
			return
		}
		compiled, compileError = goja.CompileAST(parsed, false)
	})
	if compileError != nil {
		return nil, fmt.Errorf("ignore pattern compilation: %w", compileError)
	}
	vm := goja.New()
	vm.SetParserOptions(gojaparser.WithDisableSourceMaps)
	module := vm.NewObject()
	if err := module.Set("exports", vm.NewObject()); err != nil {
		return nil, err
	}
	if err := vm.Set("module", module); err != nil {
		return nil, err
	}
	// This is only a string language input; no process methods or environment.
	if err := vm.Set("process", map[string]string{"platform": platform}); err != nil {
		return nil, err
	}
	if _, err := vm.RunProgram(compiled); err != nil {
		return nil, fmt.Errorf("ignore pattern initialization: %w", err)
	}
	factory, ok := goja.AssertFunction(module.Get("exports"))
	if !ok {
		return nil, fmt.Errorf("ignore pattern factory unavailable")
	}
	value, err := factory(goja.Undefined())
	if err != nil {
		return nil, err
	}
	object := value.ToObject(vm)
	add, addOK := goja.AssertFunction(object.Get("add"))
	test, testOK := goja.AssertFunction(object.Get("test"))
	if !addOK || !testOK {
		return nil, fmt.Errorf("ignore pattern methods unavailable")
	}
	return &Matcher{vm: vm, object: value, add: add, test: test}, nil
}

func (matcher *Matcher) Add(patterns string) error {
	matcher.mu.Lock()
	defer matcher.mu.Unlock()
	_, err := matcher.add(matcher.object, matcher.vm.ToValue(patterns))
	return err
}

func (matcher *Matcher) Test(path string) (Result, error) {
	result, _, err := matcher.TestRule(path)
	return result, err
}
func (matcher *Matcher) TestRule(path string) (Result, string, error) {
	matcher.mu.Lock()
	defer matcher.mu.Unlock()
	result, err := matcher.test(matcher.object, matcher.vm.ToValue(path))
	if err != nil {
		return Result{}, "", err
	}
	object := result.ToObject(matcher.vm)
	pattern := ""
	if rule := object.Get("rule"); rule != nil && !goja.IsUndefined(rule) && !goja.IsNull(rule) {
		pattern = rule.ToObject(matcher.vm).Get("pattern").String()
	}
	return Result{Ignored: object.Get("ignored").ToBoolean(), Unignored: object.Get("unignored").ToBoolean()}, pattern, nil
}
