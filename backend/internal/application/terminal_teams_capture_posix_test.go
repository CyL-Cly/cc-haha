//go:build !windows

package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalTeamsPaneCaptureFiles() (protocol.JSONObject, protocol.JSONObject) {
	var old protocol.JSONObject
	_ = old.UnmarshalJSON([]byte(`{"name":"application-teams","createdAt":42,"members":[{"name":"team-lead","agentId":"lead"},{"name":"held","agentId":"held@application-teams","tmuxPaneId":"%1","backendType":"tmux","isActive":false}]}`))
	next := old.Clone()
	var members []protocol.JSONObject
	_ = json.Unmarshal(old.Get("members"), &members)
	members[1].Set("tmuxPaneId", raw("%9"))
	next.Set("members", raw(members))
	return old, next
}

func TestActualTeamsRenderedMemberCaptureSurvivesNextUncommittedFileRead(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original Source Bun required")
	}
	old, next := terminalTeamsPaneCaptureFiles()
	t.Run("whole-original-rendered-member", func(t *testing.T) {
		home, _ := filepath.EvalSymlinks(t.TempDir())
		_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
		_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
		fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-teams.fixture.ts")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAMS_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_SIMPLE=1", "PATH=/usr/bin:/bin", "USER_TYPE=external"}
		cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "team": "application-teams", "file": old, "insideTmux": true, "steps": []map[string]any{{"key": "\r", "text": "@held"}, {"replaceFile": next, "key": "\r", "done": 1, "requests": 1}}}))
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal("original whole mounted Teams caller", err, string(output))
		}
		var source struct {
			Frames []struct {
				File     protocol.JSONObject
				Requests []struct{ Args []string }
			}
			Blocked []string
		}
		if json.Unmarshal(output, &source) != nil || len(source.Frames) != 3 || len(source.Blocked) != 0 {
			t.Fatal(string(output))
		}
		frame := source.Frames[2]
		if len(frame.Requests) != 1 || strings.Join(frame.Requests[0].Args, " ") != "select-pane -t %1" || !bytes.Contains(frame.File.Get("members"), []byte(`"%9"`)) {
			t.Fatal("Source captured rendered pane instead of newer physical config", string(raw(frame)))
		}
		t.Log("actual whole original Teams detail: physical config changed same agent to pane%9 before Enter; original rendered closure invokes real select-pane%1, new config retained,0blocked")
	})
	for _, commit := range []bool{false, true} {
		name := "actual-application-uncommitted-poll-read"
		if commit {
			name = "actual-application-new-commit-before-old-rendered-key"
		}
		t.Run(name, func(t *testing.T) {
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
			initial, remove, err := s.SubscribeTerminalCachedTeams(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer remove()
			events := make(chan tui.Event, 64)
			emit := func(event tui.Event) error { events <- event; return nil }
			if err = s.OpenTerminalTeams(context.Background(), tui.TerminalTeamsRequest{ID: "captured-frame", InitialTeams: initial}, emit); err != nil {
				t.Fatal(err)
			}
			panel := awaitTerminalTeamsPanel(t, events, "captured-frame")
			if err = s.ApplyLocalPanel(context.Background(), terminalTeamsPhysicalResult(panel, "teams-detail", 0), emit); err != nil {
				t.Fatal(err)
			}
			detail := awaitTerminalTeamsPanel(t, events, panel.ID)
			for {
				var p protocol.JSONObject
				_ = p.UnmarshalJSON(detail.Presentation)
				if p.String("level") == "detail" {
					break
				}
				detail = awaitTerminalTeamsPanel(t, events, panel.ID)
			}
			s.mu.Lock()
			state := s.terminalPanel
			s.mu.Unlock()
			state.mu.Lock()
			native := state.teamsPanel.native
			state.mu.Unlock()
			file := config.Read("application-teams")
			var members []protocol.JSONObject
			_ = json.Unmarshal(file.Get("members"), &members)
			members[1].Set("tmuxPaneId", raw("%9"))
			file.Set("members", raw(members))
			if err = config.Write("application-teams", *file); err != nil {
				t.Fatal(err)
			}
			// This is the production poll's real file read before its separate
			// merge obtains state.mu and commits the new frame. The UI is still
			// displaying its previous immutable detail/member projection.
			snapshot, err := native.CaptureSnapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if commit {
				s.mergeTerminalTeamsSnapshot(context.Background(), state, snapshot, nil, emit)
			}
			actual := terminalTeamsPhysicalResult(detail, "teams-view", 0)
			encoded, _ := json.Marshal(actual)
			var jsonOnly tui.LocalPanelResult
			_ = json.Unmarshal(encoded, &jsonOnly)
			if err = s.ApplyLocalPanel(context.Background(), jsonOnly, emit); !errors.Is(err, cooperative.ErrRestricted) {
				t.Fatal("JSON manufactured a captured native Teams render frame", err)
			}
			foreign, err := s.process.OpenNativeTerminalTeams(context.Background(), initial)
			if err != nil {
				t.Fatal(err)
			}
			foreignSnapshot, err := foreign.CaptureSnapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			forged := *actual.TeamsFrame.(*terminalTeamsFrame)
			forged.view = foreignSnapshot
			invalid := actual
			invalid.TeamsFrame = &forged
			if err = s.ApplyLocalPanel(context.Background(), invalid, emit); !errors.Is(err, cooperative.ErrRestricted) {
				t.Fatal("another actual dialog manufactured this mount's captured frame", err)
			}
			if err = s.ApplyLocalPanel(context.Background(), actual, emit); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				data, _ := os.ReadFile(filepath.Join(home, "pane-requests.jsonl"))
				for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
					var request struct{ Args []string }
					if json.Unmarshal([]byte(line), &request) == nil && len(request.Args) > 0 && request.Args[0] == "select-pane" {
						if strings.Join(request.Args, " ") != "select-pane -t %1" {
							t.Fatal("uncommitted FS snapshot replaced rendered pane action", request.Args)
						}
						return
					}
				}
				if time.Now().After(deadline) {
					t.Fatal("actual rendered view did not start native pane command")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}
