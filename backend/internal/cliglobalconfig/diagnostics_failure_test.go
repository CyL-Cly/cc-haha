package cliglobalconfig

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
)

func TestDiagnosticsEnableFailureRetainsOriginalGuardAndStrictRead(t *testing.T) {
	for _, stage := range []string{"started", "strict-read", "completed"} {
		t.Run(stage, func(t *testing.T) {
			home := t.TempDir()
			base := filepath.Join(home, "diagnostic", "events")
			config := []byte(`{"unknown":"original-private-value"}`)
			if stage == "strict-read" {
				config = []byte(`{broken`)
			}
			if err := os.WriteFile(filepath.Join(home, ".claude.json"), config, 0600); err != nil {
				t.Fatal(err)
			}
			logCalls, elapsedCalls := 0, 0
			process, err := diagnostics.NewProcess(diagnostics.Options{Environment: func(string) string { return base }, Now: func() time.Time {
				logCalls++
				if stage == "started" && logCalls == 1 || stage == "completed" && logCalls == 2 {
					return time.UnixMilli(8640000000000001)
				}
				return time.UnixMilli(1700000000000)
			}, NowMillis: func() int64 { return 1700000000000 }, PID: 123, Platform: "darwin", Files: diagnostics.OSFileSystem{}})
			if err != nil {
				t.Fatal(err)
			}
			service, err := New(Options{Home: home, NowMillis: func() float64 { elapsedCalls++; return float64(elapsedCalls) }, Diagnostics: process, Stderr: func(string) {}})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			err = service.Enable()
			if err == nil {
				t.Fatal("first error was swallowed")
			}
			if stage == "strict-read" {
				var parse *ConfigParseError
				if !errors.As(err, &parse) {
					t.Fatal(err)
				}
			}
			if service.enabled != (stage != "started") {
				t.Fatalf("enable publication changed: %v", service.enabled)
			}
			raw, readErr := os.ReadFile(base + ".123.current.jsonl")
			if stage == "started" {
				if !errors.Is(readErr, os.ErrNotExist) || elapsedCalls != 1 {
					t.Fatalf("started failure touched storage/completion: %s/%v/%d", raw, readErr, elapsedCalls)
				}
			} else {
				if readErr != nil || bytes.Count(raw, []byte{'\n'}) != 1 || !bytes.Contains(raw, []byte(`"enable_configs_started"`)) || bytes.Contains(raw, []byte("original-private-value")) {
					t.Fatalf("failure diagnostics leaked/added completion: %s/%v", raw, readErr)
				}
				oldLog, oldElapsed := logCalls, elapsedCalls
				if err = service.Enable(); err != nil || logCalls != oldLog || elapsedCalls != oldElapsed {
					t.Fatalf("enabled guard repeated failed work: %v/%d/%d", err, logCalls, elapsedCalls)
				}
			}
		})
	}
}

// Source elapsed Date.now reads do not call the independent new Date primitive.
func TestDiagnosticsDefaultElapsedClockDoesNotReadConfigCalendar(t *testing.T) {
	home := t.TempDir()
	calendarCalls := 0
	service, err := New(Options{Home: home, Now: func() time.Time { calendarCalls++; return time.UnixMilli(1700000000000) }, Stderr: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err = service.Enable(); err != nil {
		t.Fatal(err)
	}
	if calendarCalls != 0 {
		t.Fatalf("elapsed clock consumed config calendar: %d", calendarCalls)
	}
}
