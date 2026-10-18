package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/installationdiagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestActualDoctorPrivateFilesystemAndNPMSourceToOwnerConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	allocation, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(allocation, "home")
	config := filepath.Join(home, ".claude")
	managed := filepath.Join(home, "managed")
	bin := filepath.Join(home, "bin")
	prefix := filepath.Join(home, "private-npm")
	cwd := home
	for _, path := range []string{config, managed, bin, filepath.Join(prefix, "bin"), filepath.Join(config, "local", "node_modules", ".bin")} {
		if e = os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	global := `{"installMethod":"native","autoUpdates":false,"autoUpdatesProtectedForNative":true,"future":{"array":[1,{"keep":true}]}}`
	settings := `{"future":{"keep":"untouched"}}`
	agentsWrite(t, filepath.Join(config, ".claude.json"), global)
	agentsWrite(t, filepath.Join(config, "settings.json"), settings)
	agentsWrite(t, filepath.Join(managed, "managed-settings.json"), `{"strictPluginOnlyCustomization":["agents","new-surface"]}`)
	for _, path := range []string{filepath.Join(prefix, "bin", "claude"), filepath.Join(config, "local", "node_modules", ".bin", "claude")} {
		if e = os.WriteFile(path, []byte("untouched legacy data"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	marker := filepath.Join(home, "npm-calls")
	script := `#!/bin/sh
printf '%s\n' "$*" >> '` + marker + `'
printf '%s\n' '` + prefix + `'
`
	if e = os.WriteFile(filepath.Join(bin, "npm"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(bin, "rg"), []byte("#!/bin/sh\nprintf 'ripgrep 14.0.0\\n'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `[]`) }))
	defer releases.Close()
	env := map[string]string{"CC_HAHA_CLI_RELEASE_API": releases.URL, "HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "PATH": bin + ":/usr/bin:/bin", "SHELL": "/bin/bash", "USER_TYPE": "external", "NODE_ENV": "production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "CC_HAHA_FIXTURE_TEMP_ROOT": allocation, "BUN_OPTIONS": "--no-env-file"}
	n, e := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: cwd, Platform: "darwin", ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	n.Temp = home
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("private keychain absent")
	}
	bindCommandPrefetchFixture(t, n)
	m, e := NewMainCommand(MainCommandOptions{Arguments: []string{"doctor"}, Native: n, Platform: "darwin", Version: "2.1.288", PID: os.Getpid(), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var actual installationdiagnostics.Diagnostic
	var doctor string
	e = m.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		actual, err = m.NativeInstallationDiagnostic(ctx, scope)
		if err != nil {
			return err
		}
		doctor, err = m.Doctor(ctx, scope)
		return err
	})
	if e != nil {
		t.Fatal(e)
	}
	executable, _ := os.Executable()
	row := map[string]any{"mode": "diagnostic", "physical": true, "platform": "macos", "executable": executable, "bundled": true, "version": "2.1.288", "packageName": "claude-code-local", "cwd": cwd, "configDir": config, "managedRoot": managed, "environment": env, "global": json.RawMessage(global), "ripgrep": map[string]any{"working": true, "mode": "system", "path": filepath.Join(bin, "rg")}}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cases": []any{row}})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-installation-diagnostic.fixture.ts")
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{}
	for key, value := range env {
		child.Env = append(child.Env, key+"="+value)
	}
	child.Stdin = bytes.NewReader(payload)
	capturePublicGoCoverage(t, child)
	out, e := child.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []struct{ Diagnostic json.RawMessage }
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	raw, _ := json.Marshal(actual)
	var got, want any
	_ = json.Unmarshal(raw, &got)
	_ = json.Unmarshal(source[0].Diagnostic, &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actual native diagnostic differs Source\nGo%s\nSource%s", raw, source[0].Diagnostic)
	}
	for _, text := range []string{"Go native (2.1.288)", "disabled (CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC set)", "new-surface", "Leftover npm local installation at " + filepath.Join(home, ".claude", "local"), "Leftover npm global installation at " + filepath.Join(prefix, "bin", "claude"), "Configuration", "Agents", "MCP health"} {
		if !strings.Contains(doctor, text) {
			t.Fatal("production Doctor omits", text, doctor)
		}
	}
	if data, e := os.ReadFile(marker); e != nil || strings.Contains(string(data), "uninstall") {
		t.Fatal("Doctor must only probe NPM", e, string(data))
	}
	for path, want := range map[string]string{filepath.Join(config, "settings.json"): settings, filepath.Join(prefix, "bin", "claude"): "untouched legacy data", filepath.Join(config, "local", "node_modules", ".bin", "claude"): "untouched legacy data"} {
		data, e := os.ReadFile(path)
		if e != nil || string(data) != want {
			t.Fatal("Doctor mutated protected state", path, e, string(data))
		}
	}
	current, e := os.ReadFile(filepath.Join(config, ".claude.json"))
	if e != nil {
		t.Fatal(e)
	}
	var old, next protocol.JSONObject
	_ = old.UnmarshalJSON([]byte(global))
	_ = next.UnmarshalJSON(current)
	if !bytes.Equal(old.Get("future"), next.Get("future")) {
		t.Fatal("Doctor loses unknown persisted fields", string(current))
	}
}
