//go:build !windows

package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func releaseTerminalTeamsFIFO(path string) {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0600)
	if err == nil {
		_, _ = syscall.Write(fd, []byte("release"))
		_ = syscall.Close(fd)
	}
}
func terminalTeamsUntil(t *testing.T, predicate func() bool, label string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal(label)
		}
		time.Sleep(time.Millisecond)
	}
}
func TestActualWholeTeamsDialogParallelPrunePhysicalReadyOrderSurvivesUnmount(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned Source Bun required")
	}
	team := "parallel-teams"
	file := nativeInProcessObject([]byte(`{"name":"parallel-teams","createdAt":42,"leadAgentId":"lead@parallel-teams","members":[{"name":"team-lead","agentId":"lead@parallel-teams","tmuxPaneId":"%0"},{"name":"two","agentId":"two@parallel-teams","tmuxPaneId":"%2","backendType":"tmux","isActive":false},{"name":"four","agentId":"four@parallel-teams","tmuxPaneId":"%4","backendType":"tmux","isActive":false}],"future":{"keep":true}}`))
	task := nativeInProcessObject([]byte(`{"subject":"held owner task","description":"must wait for actual pane completion","status":"in_progress","owner":"two","blocks":[],"blockedBy":[]}`))
	home, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fifo := filepath.Join(home, "pane-release")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	defer releaseTerminalTeamsFIFO(fifo)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-teams.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAMS_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_SIMPLE=1", "PATH=/usr/bin:/bin", "USER_TYPE=external"}
	steps := []map[string]any{{"stage": "first-ready", "key": "p", "requests": 2, "removed": "four", "noticeCount": 1}, {"stage": "unmount-held", "key": "\x1b", "done": 1}, {"stage": "second-ready", "release": true, "removed": "two", "noticeCount": 2, "gonePane": "%2"}}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "team": team, "file": file, "tasks": []protocol.JSONObject{task}, "steps": steps, "insideTmux": true, "heldPane": "%2"}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Frames  []map[string]any
		Blocked []string
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Frames) != 4 || len(source.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	for _, i := range []int{1, 2} {
		frame := source.Frames[i]
		if len(frame["requests"].([]any)) != 2 || len(frame["statuses"].([]any)) != 1 || frame["statuses"].([]any)[0].(map[string]any)["name"] != "two" {
			t.Fatal("Source physical parallel admission/held peer", frame)
		}
		notice := frame["inbox"].([]any)
		if len(notice) != 1 || !strings.Contains(notice[0].(map[string]any)["text"].(string), "four was terminated") {
			t.Fatal("Source actual ready order", frame)
		}
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		terminalTeamsPrivatePanePeer(t, n.Home)
		nativeFIFO := filepath.Join(n.Home, "pane-release")
		if err := syscall.Mkfifo(nativeFIFO, 0600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { releaseTerminalTeamsFIFO(nativeFIFO) })
		n.Environment["PATH"] = filepath.Join(n.Home, "bin") + ":/usr/bin:/bin"
		n.Environment["TMUX"] = filepath.Join(n.Home, "private-tmux-socket") + ",1,0"
		n.Environment["CC_HAHA_TEAM_PANE_RECEIPT"] = filepath.Join(n.Home, "pane-requests.jsonl")
		n.Environment["CC_HAHA_TEAM_HELD_PANE"] = "%2"
		n.Environment["CC_HAHA_TEAM_PANE_RELEASE"] = nativeFIFO
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.TeammateMode = "tmux"
		o.Arguments.Tools = []string{"TeamCreate,TeamDelete,TaskList"}
		o.Error = io.Discard
	})
	defer m.Close()
	callTerminalTeamFooterActualTool(t, ctx, m, "TeamCreate", `{"team_name":"parallel-teams"}`)
	nativeFile := file.Clone()
	nativeFile.Set("leadSessionId", quoteMain(m.Controller.SessionID()))
	if err = m.teamLeader.config.Write(team, nativeFile); err != nil {
		t.Fatal(err)
	}
	if err = m.SetTeamRuntimeSnapshot(ctx, team, rawMain(42)); err != nil {
		t.Fatal(err)
	}
	if _, err = m.taskStore.Create(team, task); err != nil {
		t.Fatal(err)
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
	if _, err = dialog.Snapshot(ctx, ""); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	action := dialog.CaptureAction("teams-prune-idle", "")
	go func() { done <- action.Run(ctx) }()
	readRequests := func() []protocol.JSONObject {
		data, _ := os.ReadFile(filepath.Join(m.options.Native.Home, "pane-requests.jsonl"))
		rows := []protocol.JSONObject{}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			rows = append(rows, nativeInProcessObject([]byte(line)))
		}
		return rows
	}
	terminalTeamsUntil(t, func() bool { return len(readRequests()) == 2 }, "both actual native pane processes must start while first blocked")
	var inbox []protocol.JSONObject
	terminalTeamsUntil(t, func() bool {
		_ = m.Owner.Run(ctx, func(*cooperative.Scope) error { inbox = m.Controller.AppState().Inbox; return nil })
		return len(inbox) == 1
	}, "ready fourth actual notice before held second")
	if !strings.Contains(inbox[0].String("text"), "four was terminated") {
		t.Fatal(inbox)
	}
	status, err := dialog.Snapshot(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var members []protocol.JSONObject
	_ = json.Unmarshal(status.Get("members"), &members)
	if len(members) != 1 || members[0].String("name") != "two" {
		t.Fatal(status)
	}
	for _, request := range readRequests() {
		var args []string
		_ = json.Unmarshal(request.Get("args"), &args)
		if request.String("command") != "tmux" || len(args) != 3 || args[0] != "kill-pane" || args[1] != "-t" || (args[2] != "%2" && args[2] != "%4") {
			t.Fatal("actual native pane consumer args", request)
		}
	}
	select {
	case err := <-done:
		t.Fatal("held Source native completion replaced by already-ready peer", err)
	default:
	}
	releaseTerminalTeamsFIFO(filepath.Join(m.options.Native.Home, "pane-release"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native prune join")
	}
	if err = m.Owner.Run(ctx, func(*cooperative.Scope) error { inbox = m.Controller.AppState().Inbox; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 2 || !strings.Contains(inbox[0].String("text"), "four was terminated") || !strings.Contains(inbox[1].String("text"), "two was terminated. 1 task(s)") {
		t.Fatal("actual readiness order", inbox)
	}
	var sourceNotices []any
	sourceNotices = source.Frames[3]["inbox"].([]any)
	var actual any
	_ = json.Unmarshal(rawMain(inbox), &actual)
	if string(rawMain(terminalTeamsNormalize(actual))) != string(rawMain(terminalTeamsNormalize(sourceNotices))) {
		t.Fatal("Source/native final controlled notices", rawMain(actual), rawMain(sourceNotices))
	}
	for _, request := range readRequests() {
		var pid int
		_ = json.Unmarshal(request.Get("pid"), &pid)
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Fatal("native pane process not reaped", pid, err)
		}
	}
	all, err := m.taskStore.List(team)
	if err != nil || len(all) != 1 || all[0].String("status") != "pending" || all[0].Get("owner") != nil {
		t.Fatal("actual task reclaimed after held pane completes", all, err)
	}
	t.Log("whole original mounted Source: both pane starts before release, ready-four notice then held-two, Esc unmount retains original action; Native actual independent action same ready order and reaped peers; no sorting or sleeps in production")
}
