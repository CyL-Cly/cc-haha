package sandboxruntime

import (
	"github.com/dop251/goja"
	"strings"
)

// SessionSuffix uses only the generic ECMAScript Number radix primitive. This
// isolated VM has no host functions, filesystem, environment, network, or
// Source business code. The runtime retains Source's actual nine-digit slice.
func SessionSuffix(random float64) (string, error) {
	vm := goja.New()
	number := vm.ToValue(random)
	toString, _ := goja.AssertFunction(number.ToObject(vm).Get("toString"))
	value, e := toString(number, vm.ToValue(36))
	if e != nil {
		return "", e
	}
	text := value.String()
	if len(text) < 2 {
		text = ""
	} else {
		text = text[2:]
	}
	if len(text) > 9 {
		text = text[:9]
	}
	return strings.Join([]string{"", text, "SBX"}, "_"), nil
}
