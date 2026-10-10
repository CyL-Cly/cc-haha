package usercontext

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func contextDiagnostics(t *testing.T, options *Options, badCall int) (string, *int) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "diagnostic", "events")
	entries, elapsed := 0, new(int)
	process, err := diagnostics.NewProcess(diagnostics.Options{Environment: func(string) string { return base }, Now: func() time.Time {
		entries++
		if entries == badCall {
			return time.UnixMilli(8640000000000001)
		}
		return time.UnixMilli(1700000000000)
	}, NowMillis: func() int64 { return 1700000000000 }, PID: 123, Platform: "darwin", Files: diagnostics.OSFileSystem{}})
	if err != nil {
		t.Fatal(err)
	}
	options.Diagnostics = process
	options.NowMillis = func() float64 { *elapsed++; return float64(*elapsed * 11) }
	return base + ".123.current.jsonl", elapsed
}

func TestDiagnosticsUserContextDisabledCacheAndCompletionBeforeDate(t *testing.T) {
	options := nativeOptions(t)
	path, elapsed := contextDiagnostics(t, &options, 0)
	completionPresentBeforeDate := false
	options.Now = func() time.Time {
		raw, _ := os.ReadFile(path)
		completionPresentBeforeDate = bytes.Contains(raw, []byte(`"user_context_completed"`))
		return time.UnixMilli(1700000000000)
	}
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	first := service.GetUserContext()
	value, err := first.Await(context.Background())
	if err != nil || value.Get("claudeMd") != nil || !completionPresentBeforeDate {
		t.Fatalf("disabled context/date boundary: %v/%v", value, err)
	}
	if service.GetUserContext() != first || *elapsed != 2 {
		t.Fatal("memo hit reran producer")
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(raw, []byte(`"data":{"duration_ms":11,"claudemd_length":0,"claudemd_disabled":true}`)) || bytes.Count(raw, []byte{'\n'}) != 2 {
		t.Fatalf("disabled diagnostics: %s/%v", raw, err)
	}
}

func TestDiagnosticsUserContextRealMemoryUTF16AndFailedCompletionMemo(t *testing.T) {
	for _, badCall := range []int{0, 1, 2} {
		t.Run(string(rune('0'+badCall)), func(t *testing.T) {
			options := nativeOptions(t)
			if err := options.Environment.SetProperty(quote("CLAUDE_CODE_SIMPLE"), quote("0")); err != nil {
				t.Fatal(err)
			}
			user, _ := options.Settings.Path("userSettings", "")
			config := filepath.Dir(user)
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(user, []byte(`{"pluginConfigs":{"agents-md@builtin":{"options":{"instructionFiles":"claude-md"}}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config, "CLAUDE.md"), []byte("私有中文 😀 instruction\n"), 0600); err != nil {
				t.Fatal(err)
			}
			options.AdditionalDirectories = []string{t.TempDir()}
			options.Memory = &ExplicitUserMemory{ConfigHome: config, Profile: ExplicitUserMemoryProfile, Files: OSMemoryFiles{}}
			path, elapsed := contextDiagnostics(t, &options, badCall)
			dateCalls := 0
			options.Now = func() time.Time { dateCalls++; return time.UnixMilli(1700000000000) }
			service, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			future := service.GetUserContext()
			value, err := future.Await(context.Background())
			if service.GetUserContext() != future {
				t.Fatal("failed/successful promise not memoized")
			}
			raw, readErr := os.ReadFile(path)
			if badCall != 0 {
				if err == nil || value != nil || dateCalls != 0 {
					t.Fatalf("date failure precedence: %v/%v/%d", value, err, dateCalls)
				}
				if badCall == 1 {
					if !os.IsNotExist(readErr) || string(service.CachedClaudeMd()) != "null" || *elapsed != 1 {
						t.Fatalf("started failure ran memory: %s/%v/%d", raw, readErr, *elapsed)
					}
				} else if readErr != nil || bytes.Count(raw, []byte{'\n'}) != 1 || string(service.CachedClaudeMd()) == "null" || *elapsed != 2 {
					t.Fatalf("completion failure lost published memory: %s/%v/%d", raw, readErr, *elapsed)
				}
				return
			}
			if err != nil || readErr != nil || dateCalls != 1 {
				t.Fatalf("real memory: %v/%v", err, readErr)
			}
			var memory string
			if err = json.Unmarshal(value.Get("claudeMd"), &memory); err != nil {
				t.Fatal(err)
			}
			if string(service.CachedClaudeMd()) != string(value.Get("claudeMd")) {
				t.Fatal("classifier memory differs from real context")
			}
			lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
			var entry protocol.JSONObject
			if len(lines) != 2 {
				t.Fatalf("diagnostics %s", raw)
			}
			if err = json.Unmarshal(lines[1], &entry); err != nil {
				t.Fatal(err)
			}
			var data struct {
				Duration int  `json:"duration_ms"`
				Length   int  `json:"claudemd_length"`
				Disabled bool `json:"claudemd_disabled"`
			}
			if err = json.Unmarshal(entry.Get("data"), &data); err != nil {
				t.Fatal(err)
			}
			if data.Duration != 11 || data.Length != len(utf16.Encode([]rune(memory))) || data.Disabled || bytes.Contains(raw, []byte("私有中文")) || bytes.Contains(raw, []byte(config)) {
				t.Fatalf("UTF16/private diagnostics: %s", raw)
			}
		})
	}
}

func TestDiagnosticsUserContextMemoryCapabilityFailureHasNoCompletion(t *testing.T) {
	options := nativeOptions(t)
	if err := options.Environment.SetProperty(quote("CLAUDE_CODE_SIMPLE"), quote("0")); err != nil {
		t.Fatal(err)
	}
	path, elapsed := contextDiagnostics(t, &options, 0)
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	future := service.GetUserContext()
	if _, err = future.Await(context.Background()); err == nil {
		t.Fatal("missing producer swallowed")
	}
	raw, err := os.ReadFile(path)
	if err != nil || bytes.Count(raw, []byte{'\n'}) != 1 || *elapsed != 1 || service.GetUserContext() != future {
		t.Fatalf("failed producer diagnostics/cache: %s/%v/%d", raw, err, *elapsed)
	}
}

func TestDiagnosticsDefaultElapsedClockDoesNotAdvanceUserDate(t *testing.T) {
	options := nativeOptions(t)
	calendarCalls := 0
	options.Now = func() time.Time { calendarCalls++; return time.Date(2026, 1, calendarCalls, 0, 0, 0, 0, time.UTC) }
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.GetUserContext().Await(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calendarCalls != 1 || string(value.Get("currentDate")) != `"Today's date is 2026-01-01."` {
		t.Fatalf("elapsed clock advanced user date: calls=%d date=%s", calendarCalls, value.Get("currentDate"))
	}
}
