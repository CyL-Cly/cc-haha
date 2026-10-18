package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"testing"
)

func TestLSPDiagnosticYieldRequiresActualOwnedNativeManager(t *testing.T) {
	diagnostic := json.RawMessage(`{"type":"diagnostics","files":[],"isNew":true}`)
	var missing *Host
	if missing.retainedNativeLSPAttachment(diagnostic) {
		t.Fatal("nil host admitted diagnostic")
	}
	for _, print := range []*PrintOptions{nil, {}, {NativeProcess: true}, {NativeProcess: false, Tools: &builtintools.Service{}}, {NativeProcess: true, Tools: &builtintools.Service{}}} {
		h := &Host{options: Options{Print: print}}
		if h.retainedNativeLSPAttachment(diagnostic) {
			t.Fatal("finite/unbound service admitted native diagnostic")
		}
		for _, payload := range []json.RawMessage{json.RawMessage(`{"type":"future"}`), json.RawMessage(`null`)} {
			if h.retainedNativeLSPAttachment(payload) {
				t.Fatal("other payload widened native yield")
			}
		}
	}
}
