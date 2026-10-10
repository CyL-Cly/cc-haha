package usercontext

import (
	"embed"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dop251/goja"
	gojaparser "github.com/dop251/goja/parser"
)

//go:embed assets/picomatch/*.js assets/picomatch/LICENSE
var picomatchFiles embed.FS

const PicomatchVersion = "4.0.4"

// ExclusionMatcher is the pinned third-party glob library running inside Go.
// Its only inputs are patterns, a candidate and the native platform. No JS
// business code, Node runtime, module path, filesystem or process is exposed.
type ExclusionMatcher struct {
	mu       sync.Mutex
	vm       *goja.Runtime
	match    goja.Callable
	realpath func(string) (string, error)
}

func NewExclusionMatcher(realpath func(string) (string, error), platform string) (*ExclusionMatcher, error) {
	if realpath == nil {
		return nil, errors.New("memory excludes require original native realpath")
	}
	vm := goja.New()
	vm.SetParserOptions(gojaparser.WithDisableSourceMaps)
	navigator := vm.NewObject()
	if err := navigator.Set("platform", platform); err != nil {
		return nil, err
	}
	if err := vm.Set("navigator", navigator); err != nil {
		return nil, err
	}
	modules := map[string]*goja.Object{}
	var load func(string) (goja.Value, error)
	load = func(name string) (goja.Value, error) {
		name = strings.TrimPrefix(name, "./")
		switch name {
		case "constants", "utils", "parse", "scan", "picomatch":
		default:
			return nil, fmt.Errorf("unavailable pure picomatch module %q", name)
		}
		if module := modules[name]; module != nil {
			return module.Get("exports"), nil
		}
		raw, err := picomatchFiles.ReadFile("assets/picomatch/" + name + ".js")
		if err != nil {
			return nil, err
		}
		value, err := vm.RunScript("picomatch-"+PicomatchVersion+"/"+name+".js", "(function(module,exports,require){\n"+string(raw)+"\n})")
		if err != nil {
			return nil, err
		}
		factory, ok := goja.AssertFunction(value)
		if !ok {
			return nil, errors.New("pure picomatch factory unavailable")
		}
		module := vm.NewObject()
		exports := vm.NewObject()
		if err := module.Set("exports", exports); err != nil {
			return nil, err
		}
		modules[name] = module
		require := vm.ToValue(func(call goja.FunctionCall) goja.Value {
			value, err := load(call.Argument(0).String())
			if err != nil {
				panic(vm.NewGoError(err))
			}
			return value
		})
		if _, err := factory(goja.Undefined(), module, exports, require); err != nil {
			return nil, err
		}
		return module.Get("exports"), nil
	}
	pico, err := load("picomatch")
	if err != nil {
		return nil, err
	}
	match, ok := goja.AssertFunction(pico.ToObject(vm).Get("isMatch"))
	if !ok {
		return nil, errors.New("pure picomatch isMatch unavailable")
	}
	return &ExclusionMatcher{vm: vm, match: match, realpath: realpath}, nil
}
func (m *ExclusionMatcher) IsExcluded(path string, patterns []string) (bool, error) {
	expanded := make([]string, 0, len(patterns)*2)
	for _, pattern := range patterns {
		expanded = append(expanded, strings.ReplaceAll(pattern, "\\", "/"))
	}
	// Source appends during iteration; the realpath-resolved version stabilizes
	// on the next visit, so no second symlink interpretation is introduced.
	for i := 0; i < len(expanded); i++ {
		pattern := expanded[i]
		if !strings.HasPrefix(pattern, "/") {
			continue
		}
		prefix := pattern
		if at := strings.IndexAny(pattern, "*?{["); at >= 0 {
			prefix = pattern[:at]
		}
		directory := filepath.Dir(prefix)
		if resolved, err := m.realpath(directory); err == nil {
			resolved = strings.ReplaceAll(resolved, "\\", "/")
			if resolved != directory {
				expanded = append(expanded, resolved+pattern[len(directory):])
			}
		}
	}
	nonempty := expanded[:0]
	for _, pattern := range expanded {
		if pattern != "" {
			nonempty = append(nonempty, pattern)
		}
	}
	if len(nonempty) == 0 {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	options := m.vm.NewObject()
	if err := options.Set("dot", true); err != nil {
		return false, err
	}
	result, err := m.match(goja.Undefined(), m.vm.ToValue(strings.ReplaceAll(path, "\\", "/")), m.vm.ToValue(nonempty), options)
	if err != nil {
		return false, err
	}
	return result.ToBoolean(), nil
}

type excludeFailure struct{ err error }
