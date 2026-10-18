package plugincli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestOriginalCLIValidateActualPhysicalConsumer(t *testing.T) {
	for _, name := range []string{"valid", "warnings", "manifest-and-content-errors", "missing", "unexpected-stat", "unexpected-scan", "read-denied", "cowork"} {
		t.Run(name, func(t *testing.T) { testOriginalCLIValidate(t, name) })
	}
}
func testOriginalCLIValidate(t *testing.T, name string) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	manifest := filepath.Join(home, "plugin", ".claude-plugin", "plugin.json")
	if err = os.MkdirAll(filepath.Dir(manifest), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifest, []byte(`{"name":"fixture","version":"1.0.0","description":"actual private manifest","author":{"name":"Fixture"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	argument := "plugin"
	expectedCode := 0
	switch name {
	case "warnings":
		if err = os.WriteFile(manifest, []byte(`{"name":"Upper_Name","category":"unused"}`), 0600); err != nil {
			t.Fatal(err)
		}
	case "manifest-and-content-errors":
		expectedCode = 1
		if err = os.WriteFile(manifest, []byte(`{"name":"bad name","future":true}`), 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(home, "plugin", "commands", "broken.md")
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte("---\ndescription: [unclosed\n---\nbody"), 0600); err != nil {
			t.Fatal(err)
		}
	case "missing":
		expectedCode = 1
		argument = "absent"
	case "unexpected-stat":
		expectedCode = 2
		argument = "file-ancestor/plugin.json"
		if err = os.WriteFile(filepath.Join(home, "file-ancestor"), []byte("regular file"), 0600); err != nil {
			t.Fatal(err)
		}
	case "unexpected-scan":
		expectedCode = 2
		path := filepath.Join(home, "plugin", "agents")
		if err = os.MkdirAll(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0700) })
	case "read-denied":
		expectedCode = 1
		if err = os.Chmod(manifest, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(manifest, 0600) })
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": root, "PATH": "/usr/bin:/bin", "BUN_OPTIONS": "--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT": root, "CC_HAHA_PLUGIN_CLI_FIXTURE": "1", "NODE_ENV": "production", "USER_TYPE": "external", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}
	payload, _ := json.Marshal(map[string]any{"action": "validate", "argument": argument, "options": map[string]any{"cowork": name == "cowork"}})
	env["CC_HAHA_PLUGIN_CLI_INPUT"] = string(payload)
	fixture, _ := filepath.Abs("../../testdata/native-cli-plugins.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	proc := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	proc.Dir = home
	for k, v := range env {
		proc.Env = append(proc.Env, k+"="+v)
	}
	var out, stderr bytes.Buffer
	proc.Stdout = &out
	proc.Stderr = &stderr
	code := 0
	if err = proc.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	if code != expectedCode {
		t.Fatal("actual Source expected validation branch failed", code, expectedCode, stderr.String(), out.String())
	}
	native, err := newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	args := []string{"plugin", "validate", argument}
	if name == "cowork" {
		args = append(args, "--cowork")
	}
	m, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: args, Native: native, Platform: "darwin", Version: "999.0.0-local", PID: os.Getpid(), Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var got plugincli.Result
	err = m.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		got, e = m.PluginCommand(ctx, scope, plugincli.Command{Action: "validate", Argument: argument, Cowork: name == "cowork"})
		return e
	})
	if err != nil {
		t.Fatal("actual Go validate consumer", err)
	}
	if got.ExitCode != code || got.Stdout != out.String() || got.Stderr != stderr.String() {
		t.Fatalf("Source exit=%d stdout=%q stderr=%q Go=%+v", code, out.String(), stderr.String(), got)
	}
}
