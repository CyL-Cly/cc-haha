package cliinstallation

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDoctorVersionLockDiagnosticAgainstActualSourcePhysicalFiles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(root, "home")
	if e = os.MkdirAll(home, 0700); e != nil {
		t.Fatal(e)
	}
	executable, _ := os.Executable()
	acquired := time.Now().UnixMilli()
	contents := map[string]string{"active.lock": string(diagnosticJSON(map[string]any{"pid": os.Getpid(), "version": "2.1.288", "execPath": executable, "acquiredAt": acquired, "unknown": map[string]any{"kept": true}})), "dead.lock": string(diagnosticJSON(map[string]any{"pid": 99999999, "version": "old", "execPath": "/private/dead", "acquiredAt": acquired})), "zero.lock": string(diagnosticJSON(map[string]any{"pid": 0, "version": "zero", "execPath": "/private/zero"})), "invalid.lock": "{bad-json}", "unrelated": "protected data"}
	create := func(dir string) {
		t.Helper()
		if e = os.MkdirAll(filepath.Join(dir, "legacy.lock", "nested"), 0700); e != nil {
			t.Fatal(e)
		}
		for name, value := range contents {
			if e = os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); e != nil {
				t.Fatal(e)
			}
		}
		if e = os.Symlink(filepath.Join(dir, "active.lock"), filepath.Join(dir, "alias.lock")); e != nil {
			t.Fatal(e)
		}
	}
	sourceDir := filepath.Join(home, "source-locks")
	goState := filepath.Join(home, "go-state")
	goDir := filepath.Join(goState, "claude", "locks")
	create(sourceDir)
	create(goDir)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-doctor-locks.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(diagnosticJSON(map[string]any{"homeDir": home, "directory": sourceDir}))
	out, e := child.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var expected struct {
		Cleaned   int
		Locks     []VersionLockInformation
		Remaining []string
	}
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e, string(out))
	}
	installer, e := New(Options{Home: home, Platform: "darwin", Architecture: "arm64", Environment: map[string]string{"ENABLE_PID_BASED_VERSION_LOCKING": "true", "XDG_STATE_HOME": goState}})
	if e != nil {
		t.Fatal(e)
	}
	actual := installer.DiagnosticLocks(ctx)
	entries, e := diagnosticEntries(goDir)
	if e != nil {
		t.Fatal(e)
	}
	remaining := []string{}
	for _, entry := range entries {
		remaining = append(remaining, entry)
	}
	for i := range expected.Locks {
		expected.Locks[i].Path = strings.Replace(expected.Locks[i].Path, sourceDir, goDir, 1)
	}
	if actual.Cleaned != expected.Cleaned || !reflect.DeepEqual(actual.Locks, expected.Locks) || !reflect.DeepEqual(remaining, expected.Remaining) {
		t.Fatalf("Go%#v files%v Source%#v", actual, remaining, expected)
	}
	for _, name := range []string{"active.lock", "unrelated"} {
		body, e := os.ReadFile(filepath.Join(goDir, name))
		if e != nil || string(body) != contents[name] {
			t.Fatal("Doctor corrupted active/protected file", name, e, string(body))
		}
	}
	installer.options.Environment["ENABLE_PID_BASED_VERSION_LOCKING"] = "false"
	if result := installer.DiagnosticLocks(ctx); result.Enabled || result.Cleaned != 0 || len(result.Locks) != 0 {
		t.Fatal("disabled rollout did not preserve Source gate", result)
	}
}
func diagnosticJSON(value any) []byte { data, _ := json.Marshal(value); return data }
