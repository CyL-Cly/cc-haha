package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalTeamsPrivatePanePeer(t *testing.T, home string) {
	t.Helper()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	body := "#!/usr/bin/python3\nimport os,sys,json\nwith open(os.environ['CC_HAHA_TEAM_PANE_RECEIPT'],'a') as f:f.write(json.dumps({'pid':os.getpid(),'command':os.path.basename(sys.argv[0]),'args':sys.argv[1:]})+'\\n')\nif 'kill-pane' in sys.argv and os.environ.get('CC_HAHA_TEAM_HELD_PANE') in sys.argv:\n with open(os.environ['CC_HAHA_TEAM_PANE_RELEASE']) as f:f.read()\nprint('tmux 3.5')\nsys.exit(1 if 'kill-pane' in sys.argv and os.environ.get('CC_HAHA_TEAM_FAIL_KILL')=='1' else 0)\n"
	for _, name := range []string{"tmux", "it2"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
}
func terminalTeamsNormalize(value any) any {
	switch v := value.(type) {
	case []any:
		for i := range v {
			v[i] = terminalTeamsNormalize(v[i])
		}
		return v
	case map[string]any:
		if _, hasFrom := v["from"]; hasFrom {
			delete(v, "id")
		}
		delete(v, "output")
		delete(v, "frame")
		delete(v, "done")
		delete(v, "stage")
		for key, item := range v {
			if key == "pid" {
				v[key] = "OWNED_PID"
				continue
			}
			if key == "timestamp" {
				v[key] = "STAMP"
				continue
			}
			if key == "requestId" {
				v[key] = "SHUTDOWN_ID"
				continue
			}
			if key == "text" {
				if text, ok := item.(string); ok {
					var object any
					if json.Unmarshal([]byte(text), &object) == nil {
						normalized, _ := json.Marshal(terminalTeamsNormalize(object))
						v[key] = string(normalized)
						continue
					}
				}
			}
			v[key] = terminalTeamsNormalize(item)
		}
		return v
	}
	return value
}
func TestActualWholeTeamsDialogSourceModesMailboxPaneKillPruneAndRoster(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned Source Bun required")
	}
	team := "whole-teams"
	file := nativeInProcessObject([]byte(`{"name":"whole-teams","description":"original private team","createdAt":42,"leadAgentId":"lead@whole-teams","members":[{"name":"team-lead","agentId":"lead@whole-teams","tmuxPaneId":"%0"},{"name":"one","agentId":"one@whole-teams","tmuxPaneId":"%1","backendType":"tmux","mode":"default","prompt":"exact original prompt","isActive":true,"future":{"keep":true}},{"name":"two","agentId":"two@whole-teams","tmuxPaneId":"%2","backendType":"tmux","mode":"plan","isActive":false},{"name":"three","agentId":"three@whole-teams","tmuxPaneId":"%3","backendType":"tmux","mode":"default","isActive":true},{"name":"four","agentId":"four@whole-teams","tmuxPaneId":"%4","backendType":"tmux","mode":"default","isActive":true}],"hiddenPaneIds":["%1","%1"],"future":{"preserved":1}}`))
	tasks := []protocol.JSONObject{nativeInProcessObject([]byte(`{"subject":"owned task","description":"private task","status":"in_progress","owner":"one@whole-teams","blocks":[],"blockedBy":[]}`)), nativeInProcessObject([]byte(`{"subject":"completed task","description":"unchanged owner","status":"completed","owner":"one","blocks":[],"blockedBy":[]}`)), nativeInProcessObject([]byte(`{"subject":"idle task","description":"private idle task","status":"in_progress","owner":"two","blocks":[],"blockedBy":[]}`))}
	steps := []map[string]any{{"stage": "detail", "key": "\r", "text": "exact original prompt"}, {"stage": "single-mode", "key": "\x1b[Z", "member": "one", "mode": "acceptEdits", "mailbox": "mode_set_request", "count": 1}, {"stage": "list", "key": "\x1b[D", "text": "4 teammates"}, {"stage": "all-mode", "key": "\x1b[Z", "member": "one", "mode": "default", "mailbox": "mode_set_request", "count": 2}, {"stage": "hide-noop", "key": "h"}, {"stage": "all-hide-noop", "key": "H"}, {"stage": "shutdown", "key": "s", "member": "one", "mailbox": "shutdown_request", "count": 1}, {"stage": "kill", "key": "k", "removed": "one", "text": "3 teammates"}, {"stage": "prune", "key": "p", "removed": "two", "text": "2 teammates"}, {"stage": "third-detail", "key": "\r", "text": "@three"}, {"stage": "view", "key": "\r", "done": 1, "requests": 3}}
	// cycleAll writes sequential actual mailbox promises. The first member's
	// commit is not completion of that original handler. Observe every final
	// mailbox before comparing its fulfilled business result.
	steps[3]["mailboxes"] = []map[string]any{{"member": "one", "type": "mode_set_request", "count": 2}, {"member": "two", "type": "mode_set_request", "count": 1}, {"member": "three", "type": "mode_set_request", "count": 1}, {"member": "four", "type": "mode_set_request", "count": 1}}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-teams.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAMS_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_SIMPLE=1", "PATH=/usr/bin:/bin", "USER_TYPE=external"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "team": team, "file": file, "tasks": tasks, "steps": steps, "insideTmux": true, "failKill": true}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(out))
	}
	var source struct {
		Frames  []map[string]any
		Blocked []string
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Frames) != len(steps)+1 || len(source.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		terminalTeamsPrivatePanePeer(t, n.Home)
		n.Environment["PATH"] = filepath.Join(n.Home, "bin") + ":/usr/bin:/bin"
		n.Environment["TMUX"] = filepath.Join(n.Home, "private-tmux-socket") + ",1,0"
		n.Environment["CC_HAHA_TEAM_PANE_RECEIPT"] = filepath.Join(n.Home, "pane-requests.jsonl")
		n.Environment["CC_HAHA_TEAM_FAIL_KILL"] = "1"
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.TeammateMode = "tmux"
		o.Arguments.Tools = []string{"TeamCreate,TeamDelete,TaskList"}
		o.Error = io.Discard
	})
	defer m.Close()
	callTerminalTeamFooterActualTool(t, ctx, m, "TeamCreate", `{"team_name":"whole-teams"}`)
	nativeFile := file.Clone()
	nativeFile.Set("leadSessionId", quoteMain(m.Controller.SessionID()))
	if err := m.teamLeader.config.Write(team, nativeFile); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTeamRuntimeSnapshot(ctx, team, rawMain(42)); err != nil {
		t.Fatal(err)
	}
	if err := m.Owner.Run(ctx, func(*cooperative.Scope) error {
		_, err := m.teamLeader.panes.SelectBackend("tmux", false, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if _, err := m.taskStore.Create(team, task); err != nil {
			t.Fatal(err)
		}
	}
	initial, remove, err := m.SubscribeTerminalCachedTeams(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	dialog, err := m.OpenNativeTerminalTeams(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]any {
		value, err := dialog.Snapshot(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		all, err := m.taskStore.List(team)
		if err != nil {
			t.Fatal(err)
		}
		inboxes := map[string]any{}
		for _, name := range []string{"one", "two", "three", "four"} {
			var inbox any
			_ = json.Unmarshal(dialog.mailbox.Read(name, team), &inbox)
			inboxes[name] = inbox
		}
		var roster protocol.JSONObject
		inbox := []protocol.JSONObject{}
		if err := m.Owner.Run(ctx, func(*cooperative.Scope) error {
			roster, _ = m.teamState.snapshot.Object("teammates")
			inbox = append(inbox, m.Controller.AppState().Inbox...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(filepath.Join(m.options.Native.Home, "pane-requests.jsonl"))
		requests := []any{}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var request map[string]any
			_ = json.Unmarshal([]byte(line), &request)
			args := request["args"].([]any)
			if len(args) > 0 && args[0] == "-V" {
				continue
			}
			requests = append(requests, request)
		}
		var statuses any
		_ = json.Unmarshal(value.Get("members"), &statuses)
		return map[string]any{"statuses": statuses, "file": dialog.config.Read(team), "tasks": all, "inboxes": inboxes, "roster": roster, "inbox": inbox, "requests": requests}
	}
	compare := func(index int) {
		t.Helper()
		got := snapshot()
		want := source.Frames[index]
		delete(want, "output")
		delete(want, "frame")
		delete(want, "done")
		delete(want, "stage")
		wantFile := want["file"].(map[string]any)
		delete(wantFile, "leadSessionId")
		// Native RuntimeSnapshot uses the canonical bootstrap roster projection;
		// compare the actual surviving identities, while file/details are exact.
		rosterKeys := func(value any) []string {
			object := value.(map[string]any)
			result := []string{}
			for key := range object {
				result = append(result, key)
			}
			sort.Strings(result)
			return result
		}
		var encoded any
		_ = json.Unmarshal(rawMain(got), &encoded)
		actual := encoded.(map[string]any)
		delete(actual["file"].(map[string]any), "leadSessionId")
		actual["roster"] = rosterKeys(actual["roster"])
		want["roster"] = rosterKeys(want["roster"])
		actual = terminalTeamsNormalize(actual).(map[string]any)
		want = terminalTeamsNormalize(want).(map[string]any)
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("actual whole Teams stage%d\nGo%s\nSource%s", index, rawMain(actual), rawMain(want))
		}
	}
	compare(0)
	for i, step := range steps {
		stage := step["stage"].(string)
		action, member := "", ""
		switch stage {
		case "detail":
			_, err = dialog.Tasks(ctx, "one@"+team)
		case "single-mode":
			action, member = "teams-cycle-mode", "one@"+team
		case "all-mode":
			action = "teams-cycle-mode"
		case "hide-noop":
			action, member = "teams-hide-toggle", "one@"+team
		case "all-hide-noop":
			action = "teams-hide-all-toggle"
		case "shutdown":
			action, member = "teams-shutdown", "one@"+team
		case "kill":
			action, member = "teams-kill", "one@"+team
		case "prune":
			action = "teams-prune-idle"
		case "third-detail":
			_, err = dialog.Tasks(ctx, "three@"+team)
		case "view":
			action, member = "teams-view", "three@"+team
		}
		if action != "" {
			err = dialog.Action(ctx, action, member)
		}
		if err != nil {
			t.Fatal(stage, err)
		}
		compare(i + 1)
	}
	t.Logf("actual original whole TeamsDialog %d mounted stages: physical file, mailbox, tasks, native pane processes and conditional roster/inbox; blocked=%v", len(source.Frames), source.Blocked)
}
