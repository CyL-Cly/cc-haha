//go:build !windows

package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualTeamsDetailAgentIdentityChangeRunsOriginalTaskEffect(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original Source Bun required")
	}
	old, next := terminalTeamsPaneCaptureFiles()
	var members []protocol.JSONObject
	_ = json.Unmarshal(next.Get("members"), &members)
	members[1].Set("agentId", raw("next-held@application-teams"))
	members[1].Set("backendType", nil)
	next.Set("members", raw(members))
	tasks := []protocol.JSONObject{}
	for _, row := range []struct{ subject, owner string }{{"OLD_ACTUAL_DETAIL_TASK", "held@application-teams"}, {"NEXT_ACTUAL_DETAIL_TASK", "next-held@application-teams"}} {
		var task protocol.JSONObject
		_ = task.UnmarshalJSON(raw(map[string]any{"subject": row.subject, "description": "actual persisted task", "status": "in_progress", "owner": row.owner, "blocks": []string{}, "blockedBy": []string{}}))
		tasks = append(tasks, task)
	}
	t.Run("whole-original-one-second-refresh", func(t *testing.T) {
		home, _ := filepath.EvalSymlinks(t.TempDir())
		_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
		_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
		fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-teams.fixture.ts")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAMS_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_SIMPLE=1", "PATH=/usr/bin:/bin", "USER_TYPE=external"}
		cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "team": "application-teams", "file": old, "tasks": tasks, "insideTmux": true, "steps": []map[string]any{{"key": "\r", "text": "OLD_ACTUAL_DETAIL_TASK"}, {"replaceFile": next, "text": "NEXT_ACTUAL_DETAIL_TASK"}}}))
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal("whole original detail task effect", err, string(output))
		}
		var source struct {
			Frames  []struct{ Frame string }
			Blocked []string
		}
		if json.Unmarshal(output, &source) != nil || len(source.Frames) != 3 || len(source.Blocked) > 0 {
			t.Fatal(string(output))
		}
		if !strings.Contains(source.Frames[1].Frame, "OLD_ACTUAL_DETAIL_TASK") || strings.Contains(source.Frames[2].Frame, "OLD_ACTUAL_DETAIL_TASK") || !strings.Contains(source.Frames[2].Frame, "NEXT_ACTUAL_DETAIL_TASK") {
			t.Fatal("original detail effect used stale member identity", string(output))
		}
		t.Log("whole original detail: real1s file refresh changes same name's agentId, original effect re-reads actual task files and replaces old owner task")
	})
	t.Run("actual-application-one-second-refresh", func(t *testing.T) {
		calls := 0
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				response(w, "team", "TeamCreate", map[string]any{"team_name": "application-teams"}, "")
			} else {
				response(w, "ready", "", nil, "TEAM_READY")
			}
		}))
		t.Cleanup(peer.Close)
		s, config, home := newTerminalTeamsPhysicalSession(t, peer.URL)
		store, err := taskstore.New(taskstore.Options{ConfigDir: filepath.Join(home, ".claude"), PID: os.Getpid(), Now: time.Now})
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			if _, err = store.Create("application-teams", task); err != nil {
				t.Fatal(err)
			}
		}
		initial, remove, err := s.SubscribeTerminalCachedTeams(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer remove()
		events := make(chan tui.Event, 64)
		emit := func(event tui.Event) error { events <- event; return nil }
		if err = s.OpenTerminalTeams(context.Background(), tui.TerminalTeamsRequest{ID: "detail-identity", InitialTeams: initial}, emit); err != nil {
			t.Fatal(err)
		}
		panel := awaitTerminalTeamsPanel(t, events, "detail-identity")
		if err = s.ApplyLocalPanel(context.Background(), terminalTeamsPhysicalResult(panel, "teams-detail", 0), emit); err != nil {
			t.Fatal(err)
		}
		until := func(needle string) tui.LocalPanel {
			t.Helper()
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			var last *tui.LocalPanel
			for {
				select {
				case event := <-events:
					if event.LocalPanel != nil && event.LocalPanel.ID == panel.ID {
						last = event.LocalPanel
						if bytes.Contains(last.Presentation, []byte(needle)) {
							return *last
						}
					}
				case <-timer.C:
					if last == nil {
						t.Fatal("actual detail projection missing", needle)
					}
					t.Fatal("actual1s detail owner/task effect missing", needle, string(last.Presentation))
				}
			}
		}
		until("OLD_ACTUAL_DETAIL_TASK")
		file := config.Read("application-teams")
		file.Set("members", next.Get("members"))
		if err = config.Write("application-teams", *file); err != nil {
			t.Fatal(err)
		}
		last := until("NEXT_ACTUAL_DETAIL_TASK")
		if bytes.Contains(last.Presentation, []byte("OLD_ACTUAL_DETAIL_TASK")) {
			t.Fatal("old task survived original new-owner effect", string(last.Presentation))
		}
		if calls != 2 {
			t.Fatal("local detail read started model", calls)
		}
	})
}
