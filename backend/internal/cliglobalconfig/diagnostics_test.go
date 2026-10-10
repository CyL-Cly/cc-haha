package cliglobalconfig

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

func TestEnableDiagnosticsUseActualCallAndFirstEnableGuard(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, "diagnostic", "events")
	clockCalls := 0
	clock := func() float64 { clockCalls++; return float64(clockCalls * 10) }
	process, err := diagnostics.NewProcess(diagnostics.Options{Environment: func(string) string { return base }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, NowMillis: func() int64 { return 1700000000000 }, PID: 123, Platform: "darwin", Files: diagnostics.OSFileSystem{}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{Home: home, Now: func() time.Time { return time.UnixMilli(1700000000000) }, NowMillis: clock, Diagnostics: process, Stderr: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err = service.Enable(); err != nil {
		t.Fatal(err)
	}
	if err = service.Enable(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(base + ".123.current.jsonl")
	if err != nil {
		t.Fatalf("first enable must synchronously produce diagnostics: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) != 2 || clockCalls != 2 {
		t.Fatalf("first-enable logs/clocks %d/%d", len(lines), clockCalls)
	}
	want := []string{"enable_configs_started", "enable_configs_completed"}
	for i, line := range lines {
		var value protocol.JSONObject
		if err = json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		if value.String("event") != want[i] {
			t.Fatalf("log %d: %s", i, line)
		}
		if i == 1 && string(value.Get("data")) != `{"duration_ms":10}` {
			t.Fatalf("actual duration: %s", line)
		}
	}
}
