package heapdump

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
)

func TestActualGoRuntimeDiagnosticsAndReadableNativeHeapProfile(t *testing.T) {
	home := t.TempDir()
	start := time.Now().Add(-time.Second)
	result := Dump(Options{DesktopPath: filepath.Join(home, "Desktop"), SessionID: "actual-native-session", Version: "fixture", Now: time.Now, Capture: func() Diagnostics { return Capture(start, time.Now()) }, Mkdir: func(path string) error { return os.MkdirAll(path, 0755) }, WriteDiagnostics: func(path string, data []byte) error { return os.WriteFile(path, data, 0600) }, WriteHeap: WriteProfile}, "manual", 0)
	if !result.Success {
		t.Fatal(result.Error)
	}
	data, err := os.ReadFile(result.HeapPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := profile.ParseData(data)
	if err != nil {
		t.Fatal("not actual readable Go heap profile", err)
	}
	if err = p.CheckValid(); err != nil {
		t.Fatal(err)
	}
	if len(p.SampleType) == 0 || len(p.Mapping) == 0 {
		t.Fatal("native profile missing real sample schema/mappings")
	}
	diagRaw, err := os.ReadFile(result.DiagPath)
	if err != nil {
		t.Fatal(err)
	}
	var diag Diagnostics
	if err = json.Unmarshal(diagRaw, &diag); err != nil {
		t.Fatal(err)
	}
	if diag.Runtime != "go" || diag.RuntimeVersion == "" || diag.MemoryUsage.HeapAlloc == 0 || diag.MemoryUsage.Goroutines == 0 || diag.UptimeSeconds < 1 || diag.SessionID != "actual-native-session" || diag.CCVersion != "fixture" {
		t.Fatal("not actual process diagnostics", diag)
	}
	if bytes.Contains(diagRaw, []byte("v8HeapStats")) {
		t.Fatal("Go runtime manufactured V8 diagnostics")
	}
	for _, path := range []string{result.HeapPath, result.DiagPath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private native artifacts", path, info, err)
		}
	}
}

func TestHeapDumpSourceWorkflowSuccessFailureAndDiagnosticsFirst(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	for _, fail := range []string{"", "mkdir", "diagnostics", "heap"} {
		for _, number := range []int{0, 2} {
			t.Run(fail+string(rune('0'+number)), func(t *testing.T) {
				home := t.TempDir()
				fixture, _ := filepath.Abs("testdata/heapdump.fixture.ts")
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				cmd.Dir = home
				cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HEAP_DUMP_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
				input, _ := json.Marshal(map[string]any{"home": home, "sid": "01234567-89ab-cdef-0123-456789abcdef", "trigger": "manual", "number": number, "fail": fail})
				cmd.Stdin = bytes.NewReader(input)
				raw, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatal(err, string(raw))
				}
				var source struct {
					Events [][]json.RawMessage
					Result struct {
						Success                   bool
						HeapPath, DiagPath, Error string
					}
				}
				if err = json.Unmarshal(raw, &source); err != nil {
					t.Fatal(err, string(raw))
				}
				events := []string{}
				emit := func(stage string) error {
					events = append(events, stage)
					if stage == fail {
						return errors.New("physical " + stage + " failure")
					}
					return nil
				}
				result := Dump(Options{DesktopPath: filepath.Join(home, "Desktop"), SessionID: "01234567-89ab-cdef-0123-456789abcdef", Version: "fixture", Now: time.Now, Capture: func() Diagnostics { events = append(events, "capture"); return Diagnostics{} }, Mkdir: func(string) error { return emit("mkdir") }, WriteDiagnostics: func(string, []byte) error { return emit("diagnostics") }, WriteHeap: func(string) error {
					if err := emit("heap"); err != nil {
						return err
					}
					events = append(events, "gc")
					return nil
				}}, "manual", number)
				wanted := []string{}
				for _, entry := range source.Events {
					var stage string
					_ = json.Unmarshal(entry[0], &stage)
					wanted = append(wanted, stage)
				}
				if !reflect.DeepEqual(events, wanted) || result.Success != source.Result.Success || result.Error != source.Result.Error || strings.Replace(result.HeapPath, ".heap.pprof", ".heapsnapshot", 1) != source.Result.HeapPath || result.DiagPath != source.Result.DiagPath {
					t.Fatalf("Go%+v events%v Source%s", result, events, raw)
				}
				if !result.Success && result.Text() != "Failed to create heap dump: "+source.Result.Error {
					t.Fatal(result.Text())
				}
			})
		}
	}
}
