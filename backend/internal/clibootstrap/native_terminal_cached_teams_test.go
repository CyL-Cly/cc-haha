package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalCachedTeamsActualSourceContextGetterIncludesLeaderOnly(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	contexts := []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`{"teamName":"leader-only","teammates":{"leader":{"name":"team-lead"}}}`), json.RawMessage(`{"teamName":"two-workers","teammates":{"leader":{"name":"team-lead"},"one":{"name":"one"},"two":{"name":"two"}}}`)}
	rows := []map[string]any{}
	for _, teams := range []bool{false, true} {
		for _, inProcess := range []bool{false, true} {
			for _, context := range contexts {
				rows = append(rows, map[string]any{"teams": teams, "inProcess": inProcess, "context": context})
			}
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-team-footer.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAM_FOOTER_SOURCE=1"}
	input, _ := json.Marshal(map[string]any{"home": home, "modes": []any{}, "footers": []any{}, "cachedTeams": rows})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected struct{ CachedTeams []json.RawMessage }
	if json.Unmarshal(out, &expected) != nil || len(expected.CachedTeams) != len(rows) {
		t.Fatal(string(out))
	}
	for i, row := range rows {
		m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
			if row["teams"].(bool) {
				n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
				n.Environment["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
			} else {
				delete(n.Environment, "CC_HAHA_AGENT_TEAMS_ENABLED")
				delete(n.Environment, "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS")
			}
		}, func(o *MainPrintOptions) {
			o.Interactive = true
			o.Error = io.Discard
			if row["inProcess"].(bool) {
				o.Arguments.TeammateMode = "in-process"
			} else {
				o.Arguments.TeammateMode = "tmux"
			}
		})
		var got []protocol.JSONObject
		err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			var snapshot protocol.JSONObject
			_ = snapshot.UnmarshalJSON(row["context"].(json.RawMessage))
			if snapshot.IsObject() {
				m.teamState = &nativeTeamState{snapshot: snapshot}
			}
			var err error
			got, err = m.terminalCachedTeams(scope)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		actual, _ := json.Marshal(got)
		want, _ := protocol.ParseAndStringifyJavaScriptJSON(expected.CachedTeams[i])
		if string(actual) != string(want) {
			t.Errorf("Source cachedTeams profile%d Go%s Source%s", i, actual, want)
		}
		m.Close()
	}
}

func TestTerminalCachedTeamsActualTeamCreateDeleteAndDetachedSubscription(t *testing.T) {
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.TeammateMode = "tmux"
		o.Arguments.Tools = []string{"TeamCreate,TeamDelete"}
		o.Error = io.Discard
	})
	defer m.Close()
	changes := make(chan []protocol.JSONObject, 8)
	initial, remove, err := m.SubscribeTerminalCachedTeams(context.Background(), func(value []protocol.JSONObject) { changes <- value })
	if err != nil || len(initial) != 0 {
		t.Fatal(initial, err)
	}
	callTerminalTeamFooterActualTool(t, context.Background(), m, "TeamCreate", `{"team_name":"cached-footer-team"}`)
	var created []protocol.JSONObject
	select {
	case created = <-changes:
	case <-time.After(2 * time.Second):
		t.Fatal("Actual TeamCreate roster publication missing")
	}
	if len(created) != 1 || created[0].String("name") != "cached-footer-team" || string(created[0].Get("memberCount")) != "0" {
		t.Fatal(created)
	}
	created[0].Set("name", json.RawMessage(`"detached mutation"`))
	snapshot, otherRemove, err := m.SubscribeTerminalCachedTeams(context.Background(), nil)
	if err != nil || len(snapshot) != 1 || snapshot[0].String("name") != "cached-footer-team" {
		t.Fatal(snapshot, err)
	}
	otherRemove()
	callTerminalTeamFooterActualTool(t, context.Background(), m, "TeamDelete", `{}`)
	select {
	case removed := <-changes:
		if len(removed) != 0 {
			t.Fatal(removed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Actual TeamDelete roster publication missing")
	}
	remove()
	remove()
	callTerminalTeamFooterActualTool(t, context.Background(), m, "TeamCreate", `{"team_name":"after-unsubscribe"}`)
	select {
	case value := <-changes:
		t.Fatal("retired team subscription", value)
	default:
	}
}
