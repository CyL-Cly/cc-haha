package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeOrdinarySandboxEnabledKeepsActualStartupBoundary(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"sandbox":{"enabled":true,"enabledPlatforms":["linux"],"failIfUnavailable":true,"network":{"allowedDomains":["127.0.0.1"]}}}`)
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Tools: []string{"Read"}, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal("settings schema is supported; execution is separately guarded", e)
	}
	defer m.Close()
	e = m.StartSession(context.Background())
	if e == nil || !strings.Contains(e.Error(), "macos is not in sandbox.enabledPlatforms") {
		t.Fatal("ordinary enabled sandbox falsely admitted execution", e)
	}
	if m.initialized {
		t.Fatal("active sandbox guard ran after initializing live Harness")
	}
	if _, e = os.Stat(filepath.Join(f.config, "projects")); !os.IsNotExist(e) {
		t.Fatal("guarded startup created transcript", e)
	}
}
