package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActualInteractiveTrustedApprovalKillsOnlyLeaderOwnedPrivateTmuxPaneAgainstSource(t *testing.T) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("physical private tmux unavailable")
	}
	short, err := os.MkdirTemp("/tmp", "team-pane-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(short)
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["PATH"] = filepath.Dir(bin) + ":/usr/bin:/bin"
		n.Environment["TMUX_TMPDIR"] = short
		delete(n.Environment, "TMUX")
		if err := os.MkdirAll(n.Environment["TMUX_TMPDIR"], 0700); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Read,SendMessage"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	socket := fmt.Sprintf("claude-swarm-%d", m.options.PID)
	driver := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, bin, append([]string{"-L", socket}, args...)...)
		cmd.Dir = m.workingDirectory()
		cmd.Env = nativeAgentProcessEnvironment(m.options.Native)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	trusted, err := driver("-f", "/dev/null", "new-session", "-d", "-s", "actual-private-fixture", "-P", "-F", "#{pane_id}", "sleep 30")
	if err != nil {
		t.Fatal(err, trusted)
	}
	defer driver("kill-server")
	victim, err := driver("split-window", "-h", "-t", trusted, "-P", "-F", "#{pane_id}", "sleep 30")
	if err != nil {
		t.Fatal(err, victim)
	}
	team := "actual-interactive-0"
	callActualTeamTool(t, m, "TeamCreate", string(rawMain(map[string]string{"team_name": team})))
	roster := map[string]any{"team-lead@team": map[string]string{"name": "team-lead"}, "worker@team": map[string]string{"name": "worker"}}
	members := []map[string]any{{"agentId": "team-lead@team", "name": "team-lead", "tmuxPaneId": "", "backendType": "in-process"}, {"agentId": "worker@team", "name": "worker", "tmuxPaneId": trusted, "backendType": "tmux"}}
	body := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("shutdown_approved")}, protocol.JSONField{Name: "requestId", Value: quoteMain("actual-shutdown")}, protocol.JSONField{Name: "from", Value: quoteMain("worker")}, protocol.JSONField{Name: "timestamp", Value: quoteMain("t")}, protocol.JSONField{Name: "paneId", Value: quoteMain(victim)}, protocol.JSONField{Name: "backendType", Value: quoteMain("tmux")})
	message := objMain(rawMain(map[string]any{"from": "worker", "text": string(rawMain(body)), "timestamp": "t"}))
	task := map[string]any{"subject": "actual pane open work", "description": "full", "status": "in_progress", "owner": "worker@team", "blocks": []string{}, "blockedBy": []string{}}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		m.teamState = &nativeTeamState{snapshot: objMain(rawMain(map[string]any{"teamName": team, "leadAgentId": "team-lead@team", "teammates": roster}))}
		if err := m.teamLeader.config.Write(team, objMain(rawMain(map[string]any{"name": team, "createdAt": 1, "members": members}))); err != nil {
			return err
		}
		m.teamLeader.mailbox, err = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.taskStore.Create(team, objMain(rawMain(task))); err != nil {
		t.Fatal(err)
	}
	if err = m.teamLeader.mailbox.Write("team-lead", team, message); err != nil {
		t.Fatal(err)
	}
	if _, err = m.PollInteractiveLeaderInbox(ctx); err != nil {
		t.Fatal(err)
	}
	state := map[string]bool{}
	for {
		out, err := driver("list-panes", "-a", "-F", "#{pane_id}")
		if err != nil {
			t.Fatal(err, out)
		}
		alive := strings.Split(out, "\n")
		state["trustedAlive"], state["victimAlive"] = false, false
		for _, pane := range alive {
			if pane == trusted {
				state["trustedAlive"] = true
			}
			if pane == victim {
				state["victimAlive"] = true
			}
		}
		if !state["trustedAlive"] {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("trusted pane never killed", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if !state["victimAlive"] {
		t.Fatal("mailbox body selected an unrelated pane")
	}
	actual := []map[string]any{}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		regular := cleanInteractiveMessages(m.Controller.AppState().Inbox)
		for i := range regular {
			if regular[i].String("from") == "system" {
				regular[i].Delete("timestamp")
			}
		}
		snapshot, err := m.taskStore.Snapshot(team)
		if err != nil {
			return err
		}
		var mailbox []protocol.JSONObject
		_ = json.Unmarshal(m.teamLeader.mailbox.Read("team-lead", team), &mailbox)
		actual = append(actual, map[string]any{"regular": regular, "teammates": m.teamState.snapshot.Get("teammates"), "members": m.teamLeader.config.Read(team).Get("members"), "tasks": snapshot.Tasks, "revision": snapshot.Revision, "mailbox": cleanInteractiveMessages(mailbox), "inboxes": map[string]any{}, "permissions": []any{}, "paneState": state})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sourceMembers := []map[string]any{{"agentId": "team-lead@team", "name": "team-lead", "tmuxPaneId": "", "backendType": "in-process"}, {"agentId": "worker@team", "name": "worker", "tmuxPaneId": "TRUSTED_PANE", "backendType": "tmux"}}
	body.Set("paneId", quoteMain("VICTIM_PANE"))
	sourceMessage := message.Clone()
	sourceMessage.Set("text", quoteMain(string(rawMain(body))))
	rows := []map[string]any{{"messages": []protocol.JSONObject{sourceMessage}, "members": sourceMembers, "teammates": roster, "recipients": []string{}, "tasks": []map[string]any{task}}}
	want, _ := runInteractiveInboxSourceWithOptions(t, rows, []any{}, map[string]any{"realPaneBinary": bin})
	var a, b any
	_ = json.Unmarshal(rawMain(actual), &a)
	_ = json.Unmarshal(want, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go%s\nSource%s", rawMain(actual), want)
	}
}
