package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualMainPrintViewedIndependentTeammateMailboxAttachmentKeepsLeaderInboxPending(t *testing.T) {
	var parents, children atomic.Int32
	parked, childStarted, release, childRelease := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		close(childRelease)
	}()
	readPath := ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "# Agent Teammate Communication") {
			if children.Add(1) != 1 {
				t.Error("view interaction restarted child model")
			}
			close(childStarted)
			select {
			case <-r.Context().Done():
			case <-childRelease:
			}
			return
		}
		switch parents.Add(1) {
		case 1:
			emitNativeTaskTool(w, "view-create", "TeamCreate", map[string]any{"team_name": "actual-viewed-mailbox"})
		case 2:
			emitNativeTaskTool(w, "view-spawn", "Agent", map[string]any{"name": "worker", "prompt": "remain live while leader views the actual mailbox", "description": "actual viewed child"})
		case 3:
			close(parked)
			select {
			case <-r.Context().Done():
				return
			case <-release:
			}
			emitNativeTaskTool(w, "view-read", "Read", map[string]any{"file_path": readPath})
		case 4:
			if strings.Count(string(body), "ACTUAL_VIEWED_WORKER_MAIL") != 1 || strings.Contains(string(body), "PRIVATE_LEADER_PENDING") || strings.Contains(string(body), "view-permission-response") {
				t.Error("viewed mailbox wrong role or protocol leak", string(body))
			}
			emitPlanFixture(w, 4, "", "ACTUAL_VIEWED_CONTEXT_COMPLETE")
		case 5:
			if strings.Count(string(body), "PRIVATE_LEADER_PENDING") != 1 {
				t.Error("view consumed leader pending message", string(body))
			}
			emitPlanFixture(w, 5, "", "ACTUAL_LEADER_INBOX_AFTER_VIEW")
		default:
			t.Error("duplicate viewed/leader mailbox request", parents.Load())
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		delete(n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,Read,SendMessage"}
	})
	defer m.ShutdownSession(context.Background())
	readPath = filepath.Join(m.workingDirectory(), "viewed-read.txt")
	if err := os.WriteFile(readPath, []byte("actual viewed read boundary"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	turn := make(chan error, 1)
	go func() {
		turn <- m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "spawn one independent teammate and read the actual fixture"})}))
	}()
	for _, ready := range []<-chan struct{}{parked, childStarted} {
		select {
		case <-ready:
		case err := <-turn:
			t.Fatal("actual owner/worker not parked", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		tasks, err := m.Controller.BackgroundTasksOnOwner(scope)
		if err != nil {
			return err
		}
		id := ""
		for _, task := range tasks {
			identity, _ := task.Object("identity")
			if task.String("type") == "in_process_teammate" && identity.String("agentName") == "worker" {
				id = task.String("id")
			}
		}
		if id == "" {
			return mainCap("actual independently spawned viewed teammate")
		}
		if err := m.Controller.EnterAgentViewOnOwner(scope, id, m.options.Native.NowMillis()); err != nil {
			return err
		}
		m.options.Native.Environment["USER_TYPE"] = "ant"
		m.teamLeader.mailbox, err = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ name, text string }{{"worker", "ACTUAL_VIEWED_WORKER_MAIL"}, {"worker", `{"type":"permission_response","request_id":"view-permission-response","subtype":"error","error":"no"}`}, {"team-lead", "PRIVATE_LEADER_PENDING"}} {
		if err = m.teamLeader.mailbox.Write(entry.name, "actual-viewed-mailbox", objMain(rawMain(map[string]any{"from": "peer", "text": entry.text, "timestamp": "t", "summary": "actual view summary"}))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = m.PollInteractiveLeaderInbox(ctx); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err = <-turn:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = m.SetTeammateView(ctx, ""); err != nil {
		t.Fatal(err)
	}
	receipt, err = m.Owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error { m.options.Native.Environment["USER_TYPE"] = "external"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	active, err := m.RunLeaderInboxCycle(ctx)
	if err != nil || !active || parents.Load() != 5 || children.Load() != 1 {
		t.Fatal("actual viewed mailbox continuation", active, err, parents.Load(), children.Load())
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal("actual viewed child did not join", err)
	}
}
