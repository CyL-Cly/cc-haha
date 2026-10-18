package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourceExportStartupReleaseRecentActivityAndWarmMemo(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned Source required")
	}
	for _, profile := range []string{"warm", "warm-file-changed", "cold-file-created-late", "already-seen", "simple-cold"} {
		t.Run(profile, func(t *testing.T) {
			o := nativeSessionOptions(t, "http://127.0.0.1:9")
			o.Interactive = true
			home := o.Environment["HOME"]
			work := filepath.Join(home, "work")
			if err := os.MkdirAll(filepath.Join(work, ".git"), 0700); err != nil {
				t.Fatal(err)
			}
			n, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: o.Environment, CWD: work, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
			clock := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
			n.Now = func() time.Time { return clock }
			n.NowMillis = func() float64 { return float64(clock.UnixMilli()) }
			o.Native, o.WorkDir = n, n.CWD
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			cache := filepath.Join(config, "cache", "changelog.md")
			if err = os.MkdirAll(filepath.Dir(cache), 0700); err != nil {
				t.Fatal(err)
			}
			initial := "## 2.1.288\n- ORIGINAL_CACHED_RELEASE_A\n- ORIGINAL_CACHED_RELEASE_B\n## 2.1.287\n- ORIGINAL_CACHED_RELEASE_C\n"
			if profile == "cold-file-created-late" {
				initial = ""
			}
			if err = os.WriteFile(cache, []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}
			global := map[string]any{"projects": map[string]any{o.WorkDir: map[string]any{"hasCompletedProjectOnboarding": true}}, "future": map[string]any{"keep": 17}, "lastReleaseNotesSeen": "1.0.0"}
			if profile == "already-seen" {
				global["lastReleaseNotesSeen"] = "999.0.0"
			}
			if profile == "simple-cold" {
				o.Environment["CLAUDE_CODE_SIMPLE"] = "1"
				n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
			}
			for _, env := range []map[string]string{o.Environment, n.Environment} {
				env["CLAUDE_CODE_FORCE_FULL_LOGO"] = "1"
			}
			if err = os.WriteFile(filepath.Join(config, ".claude.json"), raw(global), 0600); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(config, "projects", projectpath.Sanitize(o.WorkDir))
			if err = os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 12; i++ {
				sid := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
				if i == 3 {
					sid = o.Arguments.SessionID
				}
				entry := map[string]any{"type": "user", "uuid": sid, "sessionId": sid, "isSidechain": i == 0, "cwd": o.WorkDir, "timestamp": clock.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": fmt.Sprintf("PRIVATE_RECENT_PROMPT_%d", i)}}
				if i == 1 {
					entry["teamName"] = "private-existing-team"
				}
				data := append(raw(entry), '\n')
				if i == 2 {
					data = append(data, append(raw(map[string]any{"type": "summary", "summary": "I apologize existing excluded summary"}), '\n')...)
				}
				if i == 4 {
					data = append(data, append(raw(map[string]any{"type": "summary", "summary": "PRIVATE_RECENT_SUMMARY"}), '\n')...)
				}
				if i == 5 {
					data = append(data, append(raw(map[string]any{"type": "last-prompt", "lastPrompt": "PRIVATE_RECENT_LATEST_PROMPT"}), '\n')...)
				}
				path := filepath.Join(directory, sid+".jsonl")
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				modified := clock.Add(-time.Duration(i+1) * time.Minute)
				if err = os.Chtimes(path, modified, modified); err != nil {
					t.Fatal(err)
				}
			}
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			changed := profile == "warm-file-changed" || profile == "cold-file-created-late"
			if changed {
				if err = os.WriteFile(cache, []byte("## 2.1.999\n- MUST_NOT_REPLACE_PROCESS_MEMO\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			presentation, err := s.process.TerminalExportPresentation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			value, err := s.process.TerminalConversation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := tui.RenderConversationText(value.Messages, presentation)
			if err = os.WriteFile(cache, []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-export-renderer.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "TZ=UTC", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_FORCE_FULL_LOGO=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_EXPORT_FIXTURE=1"}
			payload := map[string]any{"home": home, "cwd": o.WorkDir, "version": o.Version, "sid": s.process.Controller.SessionID(), "now": clock.Format(time.RFC3339Nano), "preload": profile != "simple-cold", "rows": []any{map[string]any{"messages": value.Messages, "columns": presentation.Columns}}}
			if changed {
				payload["changedChangelog"] = "## 2.1.999\n- MUST_NOT_REPLACE_PROCESS_MEMO\n"
			}
			if profile == "simple-cold" {
				command.Env = append(command.Env, "CLAUDE_CODE_SIMPLE=1")
			}
			command.Stdin = bytes.NewReader(raw(payload))
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			var source struct {
				Results []string
				Caps    struct{ Blocked []string }
			}
			if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != 1 || len(source.Caps.Blocked) > 0 {
				t.Fatal(err, string(output))
			}
			if strings.TrimRight(got, "\n") != strings.TrimRight(source.Results[0], "\n") {
				t.Fatalf("actual startup cached producer differs\nGo:\n%s\nSource:\n%s", got, source.Results[0])
			}
			if strings.Contains(got, "MUST_NOT_REPLACE_PROCESS_MEMO") {
				t.Fatal("export reread replaced original warm/cold memo")
			}
		})
	}
}
