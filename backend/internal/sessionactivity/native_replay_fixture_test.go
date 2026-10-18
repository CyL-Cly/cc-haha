package sessionactivity_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var nativeReplayBuild struct {
	sync.Once
	binary string
	error  error
	raw    []byte
}

func buildNativeReplay(t *testing.T) string {
	t.Helper()
	nativeReplayBuild.Do(func() {
		directory, e := os.MkdirTemp("", "cc-haha-native-activity-fixture-build-")
		if e != nil {
			nativeReplayBuild.error = e
			return
		}
		nativeReplayBuild.binary = filepath.Join(directory, "native-helper")
		goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
		command := exec.Command(goTool, "build", "-mod=readonly", "-o", nativeReplayBuild.binary, "./testdata/replay-fixture")
		capturePublicGoCoverage(t, command)
		nativeReplayBuild.raw, nativeReplayBuild.error = command.CombinedOutput()
	})
	if nativeReplayBuild.error != nil {
		t.Fatalf("native fixture build: %v\n%s", nativeReplayBuild.error, nativeReplayBuild.raw)
	}
	return nativeReplayBuild.binary
}
func TestMain(m *testing.M) {
	alignPublicGoCoverage()
	exit := m.Run()
	if nativeReplayBuild.binary != "" {
		_ = os.RemoveAll(filepath.Dir(nativeReplayBuild.binary))
	}
	os.Exit(exit)
}
func originalProgram(t *testing.T, id string) json.RawMessage {
	t.Helper()
	b, e := os.ReadFile("testdata/replay-fixture/original-programs.json")
	if e != nil {
		t.Fatal(e)
	}
	var v struct{ Programs []json.RawMessage }
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	for _, p := range v.Programs {
		var x struct{ ID string }
		_ = json.Unmarshal(p, &x)
		if x.ID == id {
			return p
		}
	}
	t.Fatal("missing original input", id)
	return nil
}
func runNativeOriginal(t *testing.T, id, guard string) (map[string]any, []byte, int) {
	t.Helper()
	allocation, e := os.MkdirTemp(t.TempDir(), "cc-haha-activity-go-replay-")
	if e != nil {
		t.Fatal(e)
	}
	allocation, e = filepath.EvalSymlinks(allocation)
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(allocation, "home")
	for _, n := range []string{"", ".fixture", ".claude", "tmp"} {
		if e = os.MkdirAll(filepath.Join(home, n), 0700); e != nil {
			t.Fatal(e)
		}
	}
	sentinel := filepath.Join(allocation, "external-sentinel")
	if e = os.WriteFile(sentinel, []byte("original guard sentinel\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if guard == "external-symlink" {
		if e = os.Symlink(sentinel, filepath.Join(home, "guard-link")); e != nil {
			t.Fatal(e)
		}
	}
	p := originalProgram(t, id)
	b, _ := json.Marshal(map[string]any{"profile": p, "guard": guard})
	if e = os.WriteFile(filepath.Join(home, ".fixture/input.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	var input struct{ ExternalControlUTF8 string }
	_ = json.Unmarshal(p, &input)
	command := exec.Command(buildNativeReplay(t))
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "TZ=UTC", "PATH=/usr/bin:/bin"}
	command.Stdin = strings.NewReader(input.ExternalControlUTF8)
	capturePublicGoCoverage(t, command)
	out, e := command.CombinedOutput()
	exit := 0
	if e != nil {
		if status, ok := e.(*exec.ExitError); ok {
			exit = status.ExitCode()
		} else {
			t.Fatalf("native start: %v", e)
		}
	}
	raw, e := os.ReadFile(filepath.Join(home, ".fixture/result.json"))
	if e != nil {
		t.Fatalf("missing native result exit=%d: %s\n%v", exit, out, e)
	}
	var result map[string]any
	if e = json.Unmarshal(raw, &result); e != nil {
		t.Fatal(e)
	}
	if b, e = os.ReadFile(sentinel); e != nil || string(b) != "original guard sentinel\n" {
		t.Fatal("guard sentinel changed", e)
	}
	return result, out, exit
}
func nativeOperations(result map[string]any, op string) []map[string]any {
	rows := []map[string]any{}
	for _, v := range result["trace"].([]any) {
		r := v.(map[string]any)
		if r["operation"] == op {
			rows = append(rows, r)
		}
	}
	return rows
}
func TestNativeReplayFilesystemGuards(t *testing.T) {
	for _, guard := range []string{"outside-read", "external-symlink"} {
		t.Run(guard, func(t *testing.T) {
			r, _, exit := runNativeOriginal(t, "disabled-zero-clock", guard)
			if exit != 2 || r["productionEntered"] != false || r["failure"].(map[string]any)["name"] != "CapabilityError" {
				t.Fatal(r, exit)
			}
		})
	}
}
func TestNativeReplayUnavailableTransportGuards(t *testing.T) {
	for _, guard := range []string{"offorigin", "native-spawn"} {
		t.Run(guard, func(t *testing.T) {
			r, _, exit := runNativeOriginal(t, "disabled-zero-clock", guard)
			if exit != 2 || r["productionEntered"] != false || r["failure"] == nil {
				t.Fatal(r, exit)
			}
		})
	}
}
func TestNativeReplayOriginalActivityReasonMapAndClock(t *testing.T) {
	for _, id := range []string{"two-reasons-delete-reinsert", "mismatched-stop-independent-reason-map", "Date-now-throw-before-cleanup-registration", "disabled-zero-clock", "late-cleanup-backward-time", "native-storage-prepare-fault-swallowed"} {
		t.Run(id, func(t *testing.T) {
			r, _, exit := runNativeOriginal(t, id, "")
			if exit != 0 || r["bodyEnded"] != true || r["failure"] != nil {
				t.Fatal(r, exit)
			}
			if id == "Date-now-throw-before-cleanup-registration" {
				rows := nativeOperations(r, "caller.operation.throw")
				if len(rows) != 1 || rows[0]["error"].(map[string]any)["identity"] != "original-error-now" {
					t.Fatal(rows)
				}
			}
			if id == "disabled-zero-clock" {
				p := r["primitiveCalls"].(map[string]any)
				if p["now"] != float64(1) || p["date"] != float64(0) {
					t.Fatal(p)
				}
			}
			if id == "native-storage-prepare-fault-swallowed" {
				// Source's original setup calls writeFileSync without a mode. The
				// native default is 0666 adjusted by the actual process umask.
				// Derive that physical mode locally; no captured Source output is used.
				reference := filepath.Join(t.TempDir(), "native-default-mode")
				if e := os.WriteFile(reference, nil, 0666); e != nil {
					t.Fatal(e)
				}
				want, e := os.Stat(reference)
				if e != nil {
					t.Fatal(e)
				}
				got, e := os.Stat(filepath.Join(r["home"].(string), "diagnostics"))
				if e != nil || !got.Mode().IsRegular() || got.Mode().Perm() != want.Mode().Perm() {
					t.Fatalf("original native file-parent setup mode: got=%v want=%v error=%v", got, want.Mode().Perm(), e)
				}
			}
		})
	}
}
func TestNativeReplayOriginalAsyncMapAndSyncThrow(t *testing.T) {
	for _, id := range []string{"registry-async-throw-second-starts", "registry-sync-throw-map-stops", "activity-serialize-throw-async-map-continues", "activity-invalid-date-async-map-continues"} {
		t.Run(id, func(t *testing.T) {
			r, _, exit := runNativeOriginal(t, id, "")
			if exit != 0 || r["bodyEnded"] != true || r["failure"] != nil {
				t.Fatal(r, exit)
			}
			rows := nativeOperations(r, "original.cleanupCallback.invoke")
			if id == "registry-sync-throw-map-stops" {
				if len(rows) != 1 || rows[0]["name"] != "first" {
					t.Fatal(rows)
				}
			} else if len(rows) < 1 {
				t.Fatal(rows)
			}
			if len(nativeOperations(r, "observer.cleanup.Then.rejected")) != 1 {
				t.Fatal("original rejection missing", r)
			}
		})
	}
}
func TestNativeReplayOriginalPendingControlsAndNoDrain(t *testing.T) {
	for _, id := range []string{"registry-all-async-start-before-external-fulfill", "registry-all-async-start-before-external-reject", "registry-pending-not-drained-after-second-reject"} {
		t.Run(id, func(t *testing.T) {
			r, out, exit := runNativeOriginal(t, id, "")
			if exit != 0 || r["bodyEnded"] != true || r["failure"] != nil {
				t.Fatal(r, exit)
			}
			if !bytes.Contains(out, []byte("originalCallbackReady")) {
				t.Fatal("original pending callback not entered", string(out))
			}
			if len(nativeOperations(r, "original.cleanupCallback.invoke")) != 2 {
				t.Fatal("later callback did not start", r)
			}
		})
	}
}
func TestNativeReplayOriginalCallbackTimerProgramsRemainTypedCapabilities(t *testing.T) {
	for _, id := range []string{"registered-timers-cleared-by-real-unregister"} {
		t.Run(id, func(t *testing.T) {
			r, _, exit := runNativeOriginal(t, id, "")
			if exit != 2 || r["bodyEnded"] != false || len(r["unsupported"].([]any)) != 1 || r["failure"].(map[string]any)["name"] != "CapabilityError" {
				t.Fatal(r, exit)
			}
		})
	}
}

func TestNativeBunOriginalCallbackRegistrationAndSignal(t *testing.T) {
	for _, id := range []string{"register-without-start-no-activity-cleanup", "native-signal-truthy-trim-without-timer"} {
		t.Run(id, func(t *testing.T) {
			r, _, exit := runNativeOriginal(t, id, "")
			if exit != 0 || r["bodyEnded"] != true || r["failure"] != nil || len(r["unsupported"].([]any)) != 0 {
				t.Fatal("original non-timer callback program not completed", r, exit)
			}
			want := 0
			if id == "native-signal-truthy-trim-without-timer" {
				want = 1
			}
			if got := len(nativeOperations(r, "original.activityCallback.invoke")); got != want {
				t.Fatalf("original keepalive environment callback count: got=%d want=%d", got, want)
			}
		})
	}
}
