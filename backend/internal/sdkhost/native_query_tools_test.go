package sdkhost

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
)

func TestNativeInteractiveQueryRefreshCannotBeEnabledByFiniteDeclarations(t *testing.T) {
	for _, print := range []*PrintOptions{nil, {}, {NativeInteractive: true}, {NativeProcess: true, NativeInteractive: true}, {NativeProcess: true, NativeInteractive: true, AgentID: "child", Tools: &builtintools.Service{}}, {NativeProcess: true, NativeInteractive: true, Tools: &builtintools.Service{}}} {
		h := &Host{options: Options{Print: print}}
		if h.nativeInteractiveQueryTools() {
			t.Fatal("unbound/finite/child admitted native REPL tools refresh")
		}
		if err := h.beginNativeQueryTools(nil)(); err != nil || h.nativeQueryTools != nil {
			t.Fatal("finite host installed query refresh consumer", err)
		}
	}
}
