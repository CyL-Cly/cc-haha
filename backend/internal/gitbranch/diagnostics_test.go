package gitbranch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func gitDiagnostics(t *testing.T, badCall int) (*diagnostics.Process, string, *int) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "diagnostic", "events")
	entries := new(int)
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
	return process, base + ".123.current.jsonl", entries
}

func TestDiagnosticsFindRootRealStatMemoAndFoundLogCatchBoundary(t *testing.T) {
	for _, badCall := range []int{0, 2} {
		t.Run(string(rune('0'+badCall)), func(t *testing.T) {
			process, path, entries := gitDiagnostics(t, badCall)
			elapsed, stats := 0, 0
			h := newHarness(t, func(options *Options) {
				options.Diagnostics = process
				options.Now = func() int64 { elapsed++; return int64(elapsed * 10) }
				options.StatSync = func(path string) (fs.FileInfo, error) { stats++; return os.Stat(path) }
			})
			outer := filepath.Join(h.home, "outer")
			inner := filepath.Join(outer, "inner")
			for _, dir := range []string{filepath.Join(outer, ".git"), filepath.Join(inner, ".git")} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			err := h.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				root, err := h.s.FindRoot(scope, inner)
				if err != nil {
					return err
				}
				want := inner
				if badCall == 2 {
					want = outer
				}
				if root != want {
					return fmt.Errorf("found-log error no longer continued ancestor: %s, want %s", root, want)
				}
				oldElapsed, oldStats, oldEntries := elapsed, stats, *entries
				cached, err := h.s.FindRoot(scope, inner)
				if err != nil || cached != root || elapsed != oldElapsed || stats != oldStats || *entries != oldEntries {
					return fmt.Errorf("memo hit changed effects: %s/%v", cached, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
			if len(lines) != 2 {
				t.Fatalf("logs %s", raw)
			}
			var entry protocol.JSONObject
			if err = json.Unmarshal(lines[1], &entry); err != nil {
				t.Fatal(err)
			}
			want := `{"duration_ms":10,"stat_count":1,"found":true}`
			if badCall == 2 {
				want = `{"duration_ms":20,"stat_count":2,"found":true}`
			}
			if string(entry.Get("data")) != want || bytes.Contains(raw, []byte(h.home)) {
				t.Fatalf("real stat/clock diagnostics: %s", raw)
			}
		})
	}
}

func TestDiagnosticsFindRootStartedAndNotFoundErrorsDoNotPublishMemo(t *testing.T) {
	for _, badCall := range []int{1, 2} {
		t.Run(string(rune('0'+badCall)), func(t *testing.T) {
			process, _, _ := gitDiagnostics(t, badCall)
			stats := 0
			h := newHarness(t, func(options *Options) {
				options.Diagnostics = process
				// The declared external filesystem profile has no .git at any level.
				options.StatSync = func(string) (fs.FileInfo, error) { stats++; return nil, os.ErrNotExist }
			})
			err := h.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				root, err := h.s.FindRoot(scope, h.home)
				if err == nil || root != "" || len(h.s.roots) != 0 {
					return fmt.Errorf("outside-catch date error was swallowed/cached: %s/%v/%d", root, err, len(h.s.roots))
				}
				if badCall == 1 && stats != 0 || badCall == 2 && stats == 0 {
					return fmt.Errorf("first failure precedence stats=%d", stats)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
