package clistartupenv

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func startupDiagnostics(t *testing.T, service *Service, badCall int) (string, *int, *int) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "diagnostic", "events")
	elapsed, entries := new(int), new(int)
	process, err := diagnostics.NewProcess(diagnostics.Options{Environment: func(string) string { return base }, Now: func() time.Time {
		*entries++
		if *entries == badCall {
			return time.UnixMilli(8640000000000001)
		}
		return time.UnixMilli(1700000000000)
	}, NowMillis: func() int64 { return 1700000000000 }, PID: 123, Platform: "darwin", Files: diagnostics.OSFileSystem{}})
	if err != nil {
		t.Fatal(err)
	}
	service.options.Diagnostics = process
	service.options.NowMillis = func() float64 { *elapsed++; return float64(*elapsed * 7) }
	return base + ".123.current.jsonl", elapsed, entries
}

func TestDiagnosticsSettingsCountRealDistinctFilesErrorsAndCacheIdentity(t *testing.T) {
	service, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	user, _ := service.options.Settings.Path("userSettings", "")
	project, _ := service.options.Settings.Path("projectSettings", "")
	if err := os.WriteFile(user, []byte(`{"env":{"PRIVATE_VALUE":"unchanged"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project, []byte(`{broken`), 0600); err != nil {
		t.Fatal(err)
	}
	service.options.Host.FlagPath = &user
	path, elapsed, _ := startupDiagnostics(t, service, 0)
	first, err := service.GetInitialSettings()
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.GetInitialSettings()
	if err != nil || first != second || *elapsed != 2 {
		t.Fatalf("cache hit reloaded or cloned: %v/%d", err, *elapsed)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("logs %s", raw)
	}
	var last protocol.JSONObject
	if err = json.Unmarshal(lines[1], &last); err != nil {
		t.Fatal(err)
	}
	if last.String("event") != "settings_load_completed" || string(last.Get("data")) != `{"duration_ms":7,"source_count":3,"error_count":1}` {
		t.Fatalf("actual settings counts: %s", lines[1])
	}
	if bytes.Contains(raw, []byte("PRIVATE_VALUE")) || bytes.Contains(raw, []byte(service.options.ConfigHome)) {
		t.Fatalf("diagnostic contained private values/paths: %s", raw)
	}
	service.options.SettingsState.ResetSettingsCache()
	if _, err = service.GetInitialSettings(); err != nil || *elapsed != 4 {
		t.Fatalf("real reset did not load again: %v/%d", err, *elapsed)
	}
}

func TestDiagnosticsSettingsFailurePrecedesSessionCachePublication(t *testing.T) {
	for _, badCall := range []int{1, 2} {
		t.Run(string(rune('0'+badCall)), func(t *testing.T) {
			service, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
			path, elapsed, _ := startupDiagnostics(t, service, badCall)
			if _, err := service.GetInitialSettings(); err == nil {
				t.Fatal("diagnostic date error swallowed")
			}
			if service.options.SettingsState.GetSessionSettingsCache() != nil {
				t.Fatal("failed load published merged cache")
			}
			raw, err := os.ReadFile(path)
			if badCall == 1 {
				if !os.IsNotExist(err) || *elapsed != 1 {
					t.Fatalf("started failure advanced loader: %s/%v/%d", raw, err, *elapsed)
				}
			} else if err != nil || bytes.Count(raw, []byte{'\n'}) != 1 || *elapsed != 2 {
				t.Fatalf("completed failure: %s/%v/%d", raw, err, *elapsed)
			}
		})
	}
}
