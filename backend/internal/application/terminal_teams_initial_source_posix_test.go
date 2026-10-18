//go:build !windows

package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type counterfeitTerminalTeamsInitial struct{ called *bool }

func (frame counterfeitTerminalTeamsInitial) InitialTeams() []protocol.JSONObject {
	*frame.called = true
	return []protocol.JSONObject{}
}

func TestActualTeamsCapturedFooterInitialTeamSurvivesContextRemovalBeforeRead(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original Source Bun required")
	}
	var file protocol.JSONObject
	_ = file.UnmarshalJSON([]byte(`{"name":"application-teams","createdAt":42,"leadAgentId":"lead@application-teams","members":[{"name":"team-lead","agentId":"lead@application-teams","tmuxPaneId":"%0","backendType":"tmux"}]}`))
	t.Run("whole-original-mounted-dialog-and-TeamDelete", func(t *testing.T) {
		home, _ := filepath.EvalSymlinks(t.TempDir())
		_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
		_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
		fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-teams.fixture.ts")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAMS_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_SIMPLE=1", "PATH=/usr/bin:/bin", "USER_TYPE=external", "CC_HAHA_AGENT_TEAMS_ENABLED=1", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1"}
		cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "team": "application-teams", "file": file, "insideTmux": true, "capturedFooterCaller": true, "steps": []map[string]any{{"deleteTeam": true}}}))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal("actual original mounted TeamDelete", err, string(out))
		}
		var source struct {
			Frames []struct {
				Frame  string
				File   json.RawMessage
				Roster json.RawMessage
			}
			Blocked []string
		}
		if json.Unmarshal(out, &source) != nil || len(source.Frames) != 2 || len(source.Blocked) > 0 {
			t.Fatal(string(out))
		}
		last := source.Frames[1]
		if string(last.File) != "null" || len(last.Roster) > 0 || !strings.Contains(last.Frame, "Team application-teams") || !strings.Contains(last.Frame, "No teammates") {
			t.Fatal("original mounted initial state followed later empty parent props", string(out))
		}
		t.Log("whole original mounted Teams + actual TeamDelete: team file/context removed, parent new initialTeams=[], dialog retains original captured team name/empty members,0blocked")
	})
	t.Run("actual-application-captured-Footer", func(t *testing.T) {
		calls := 0
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			calls++
			switch calls {
			case 1:
				response(w, "team", "TeamCreate", map[string]any{"team_name": "application-teams"}, "")
			case 2:
				response(w, "ready", "", nil, "TEAM_READY")
			case 3:
				if !bytes.Contains(body, []byte("DELETE_CAPTURED_TEAM")) {
					t.Error(string(body))
				}
				response(w, "delete", "TeamDelete", map[string]any{}, "")
			case 4:
				response(w, "deleted", "", nil, "CAPTURED_TEAM_DELETED")
			default:
				t.Error("unowned model", calls)
			}
		}))
		t.Cleanup(peer.Close)
		s, config, _ := newTerminalTeamsPhysicalSession(t, peer.URL)
		actualFile := config.Read("application-teams")
		var members []protocol.JSONObject
		_ = json.Unmarshal(actualFile.Get("members"), &members)
		actualFile.Set("members", raw(members[:1]))
		if err := config.Write("application-teams", *actualFile); err != nil {
			t.Fatal(err)
		}
		if err := s.process.SetTeamRuntimeSnapshot(context.Background(), "application-teams", actualFile.Get("createdAt")); err != nil {
			t.Fatal(err)
		}
		frame, remove, err := s.SubscribeTerminalCachedTeamsFrames(context.Background(), nil)
		if err != nil || frame == nil || len(frame.InitialTeams()) != 1 {
			t.Fatal(frame, err)
		}
		defer remove()
		initial := frame.InitialTeams()
		other, err := NewSession(context.Background(), nativeSessionOptions(t, peer.URL))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = other.Close(context.Background()) })
		if _, err = other.process.OpenNativeTerminalTeamsInitial(context.Background(), frame.(*clibootstrap.NativeTerminalTeamsInitial)); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("foreign actual MainPrint accepted cached Footer claim", err)
		}
		called := false
		if err = s.OpenTerminalTeams(context.Background(), tui.TerminalTeamsRequest{ID: "counterfeit", InitialTeams: initial, InitialFrame: counterfeitTerminalTeamsInitial{&called}}, nil); !errors.Is(err, cooperative.ErrRestricted) || called {
			t.Fatal("arbitrary callback issued cached Footer claim", err, called)
		}
		tampered := frame.InitialTeams()
		tampered[0].Set("name", raw("unissued-team"))
		if err = s.OpenTerminalTeams(context.Background(), tui.TerminalTeamsRequest{ID: "tampered", InitialTeams: tampered, InitialFrame: frame}, nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("JSON changed actual cached claim", err)
		}
		s.RetireTerminalTeams("retired-initial")
		if err = s.OpenTerminalTeams(context.Background(), tui.TerminalTeamsRequest{ID: "retired-initial", InitialTeams: initial, InitialFrame: frame}, nil); !errors.Is(err, context.Canceled) {
			t.Fatal("retired cached Footer mount was admitted", err)
		}
		if err = s.Submit(context.Background(), tui.Input{Text: "DELETE_CAPTURED_TEAM"}, nil); err != nil {
			t.Fatal(err)
		}
		if config.Read("application-teams") != nil || calls != 4 {
			t.Fatal("actual TeamDelete did not clear physical team", calls)
		}
		if _, err = s.process.OpenNativeTerminalTeams(context.Background(), initial); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("nil claim bypassed original fresh cached team guard", err)
		}
		events := make(chan tui.Event, 16)
		if err = s.OpenTerminalTeams(context.Background(), tui.TerminalTeamsRequest{ID: "captured-initial", InitialTeams: initial, InitialFrame: frame}, func(event tui.Event) error { events <- event; return nil }); err != nil {
			t.Fatal(err)
		}
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case event := <-events:
				if event.LocalPanel != nil {
					var presentation protocol.JSONObject
					_ = presentation.UnmarshalJSON(event.LocalPanel.Presentation)
					if presentation.String("error") != "" {
						t.Fatal("later parent context invalidated already captured Footer mount", string(event.LocalPanel.Presentation))
					}
					if presentation.Get("loading") != nil && string(presentation.Get("loading")) == "false" {
						if event.LocalPanel.Title != "Team application-teams" || len(event.LocalPanel.Rows) != 0 {
							t.Fatal(*event.LocalPanel)
						}
						if err = s.Close(context.Background()); err != nil {
							t.Fatal(err)
						}
						if err = s.OpenTerminalTeams(context.Background(), tui.TerminalTeamsRequest{ID: "closed-initial", InitialTeams: initial, InitialFrame: frame}, nil); !errors.Is(err, context.Canceled) {
							t.Fatal("closed root accepted captured Footer claim", err)
						}
						return
					}
				}
			case <-timer.C:
				t.Fatal("actual captured Footer read did not settle")
			}
		}
	})
}
