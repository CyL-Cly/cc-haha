package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualSourceSandboxInitializationFailureDoesNotAbortHostAndCanRetry(t *testing.T) {
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/sandbox-initialize.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = strings.NewReader(`{"rows":[{"failure":"convert"},{"failure":"manager"}]}`)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var source []struct {
		Before struct {
			WrapError                                                            string
			InitializeCalls, WrapCalls, SubscriptionCalls, DetectCalls, Converts int
			Logs                                                                 []string
		}
		After struct {
			InitializeCalls, WrapCalls, SubscriptionCalls, DetectCalls, Converts int
			Wrapped                                                              string
			Logs                                                                 []string
		}
	}
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	if len(source) != 2 || source[0].Before.WrapError != "Sandbox failed to initialize. " || source[0].Before.InitializeCalls != 0 || source[0].After.InitializeCalls != 1 || source[0].After.DetectCalls != 1 || source[0].After.SubscriptionCalls != 1 || source[0].After.Wrapped != "second" || source[1].Before.InitializeCalls != 1 || source[1].After.InitializeCalls != 2 {
		t.Fatal("actual Source initialization contract", string(output))
	}
	f := newNativePluginFixture(t)
	f.n.Environment["PATH"] = "/opt/homebrew/bin:/usr/bin:/bin"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["SHELL"] = "/bin/bash"
	path := filepath.Join(f.config, "settings.json")
	f.write(t, path, `{"sandbox":{"enabled":true,"allowUnsandboxedCommands":false,"filesystem":{"allowWrite":["null\u0000path"]}}}`)
	disabled := false
	var terminal bytes.Buffer
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 100, Tools: []string{"Bash"}, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Interactive: true, Input: strings.NewReader(""), Output: io.Discard, Error: &terminal})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal("Source catches initialization failure; host must remain usable", e)
	}
	if !m.initialized || m.sandbox == nil || m.sandbox.manager != nil || terminal.Len() != 0 {
		t.Fatal("failed sandbox must not claim ready or print ordinary warning", m.initialized, terminal.String())
	}
	result, e := m.RunTerminalBash(context.Background(), "printf forbidden > must-not-exist", nil)
	if e != nil || result.Stderr != "Command failed: "+source[0].Before.WrapError {
		t.Fatal("failed sandbox must use original terminal local-error projection", e, result)
	}
	if _, e = os.Stat(filepath.Join(f.cwd, "must-not-exist")); !os.IsNotExist(e) {
		t.Fatal("failed sandbox escaped", e)
	}
	f.write(t, path, `{"sandbox":{"enabled":true,"allowUnsandboxedCommands":false}}`)
	if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		m.settingState.ResetSettingsCache()
		return m.initializeNativeSandbox(scope)
	}); e != nil {
		t.Fatal(e)
	}
	if m.sandbox.manager == nil {
		t.Fatal("Source retry did not initialize actual manager")
	}
	first := m.sandbox.manager
	if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { return m.initializeNativeSandbox(scope) }); e != nil || m.sandbox.manager != first {
		t.Fatal("duplicate initialize replaced owned manager", e)
	}
	if e = m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
}
