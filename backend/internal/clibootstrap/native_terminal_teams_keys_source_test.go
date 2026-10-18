package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualWholeTeamsDialogDetailArrowAndListShrinkKeyClosures(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original Source Bun required")
	}
	file := nativeInProcessObject([]byte(`{"name":"keys","createdAt":42,"members":[{"name":"team-lead","agentId":"lead"},{"name":"one","agentId":"one@keys","tmuxPaneId":"%1","backendType":"tmux","mode":"default"},{"name":"two","agentId":"two@keys","tmuxPaneId":"%2","backendType":"tmux","mode":"default"},{"name":"three","agentId":"three@keys","tmuxPaneId":"%3","backendType":"tmux","mode":"default"}]}`))
	shrink := nativeInProcessObject([]byte(`{"name":"keys","createdAt":42,"members":[{"name":"team-lead","agentId":"lead"},{"name":"one","agentId":"one@keys","tmuxPaneId":"%1","backendType":"tmux","mode":"default"}]}`))
	for _, profile := range []string{"detail-arrow-retains-current-member", "list-refresh-retains-out-of-range-index"} {
		t.Run(profile, func(t *testing.T) {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
			_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-teams.fixture.ts")
			steps := []map[string]any{{"key": "\x1b[B"}, {"key": "\x1b[B"}}
			if profile == "detail-arrow-retains-current-member" {
				steps = append(steps, map[string]any{"key": "\r", "text": "@three"}, map[string]any{"key": "\x1b[B"}, map[string]any{"key": "k", "removed": "three", "requests": 1, "text": "2 teammates"})
			} else {
				steps = append(steps, map[string]any{"replaceFile": shrink, "text": "1 teammate"}, map[string]any{"key": "\x1b[Z", "member": "one", "mode": "acceptEdits", "mailbox": "mode_set_request", "count": 1}, map[string]any{"key": "\r"}, map[string]any{"key": "k"})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAMS_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_SIMPLE=1", "PATH=/usr/bin:/bin", "USER_TYPE=external"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "team": "keys", "file": file, "insideTmux": true, "steps": steps}))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal("whole original key consumer", err, string(out))
			}
			var source struct {
				Frames []struct {
					Requests []struct{ Args []string }
					File     json.RawMessage
					Done     int
				}
				Blocked []string
			}
			if json.Unmarshal(out, &source) != nil || len(source.Frames) != len(steps)+1 || len(source.Blocked) > 0 {
				t.Fatal(string(out))
			}
			last := source.Frames[len(source.Frames)-1]
			if profile == "detail-arrow-retains-current-member" {
				if len(last.Requests) != 1 || strings.Join(last.Requests[0].Args, " ") != "kill-pane -t %3" || bytes.Contains(last.File, []byte(`"three"`)) {
					t.Fatal("detail arrow changed original current member", string(out))
				}
			} else if len(last.Requests) != 0 || last.Done != 0 || !bytes.Contains(last.File, []byte(`"acceptEdits"`)) || !bytes.Contains(last.File, []byte(`"one"`)) {
				t.Fatal("out-of-range index invoked a row or prevented original bulk mode", string(out))
			}
			t.Log(profile, "whole original mounted dialog, actual private config/mailbox/native process,0blocked")
		})
	}
}
