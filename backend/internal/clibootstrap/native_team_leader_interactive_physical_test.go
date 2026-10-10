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

func TestActualInteractiveMainPrintBusyOwnerPollFeedsMidTurnAttachmentExactlyOnce(t *testing.T) {
	var posts atomic.Int32
	parked, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	readPath := ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch posts.Add(1) {
		case 1:
			emitNativeTaskTool(w, "interactive-create", "TeamCreate", map[string]any{"team_name": "actual-interactive-midturn"})
		case 2:
			close(parked)
			select {
			case <-r.Context().Done():
				return
			case <-release:
			}
			emitNativeTaskTool(w, "interactive-read", "Read", map[string]any{"file_path": readPath})
		case 3:
			if strings.Count(string(body), "ACTUAL_BUSY_PEER_ARTIFACT") != 1 || !strings.Contains(string(body), `summary=\"actual busy summary\"`) || !strings.Contains(string(body), `color=\"blue\"`) || strings.Contains(string(body), "permission_request") {
				t.Error("actual attachment request missing/doubled/leaked", string(body))
			}
			emitPlanFixture(w, 3, "", "ACTUAL_MIDTURN_DELIVERED")
		default:
			t.Error("processed attachment caused extra model request", posts.Load())
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		delete(n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Read,SendMessage"}
	})
	readPath = filepath.Join(m.workingDirectory(), "midturn-read.txt")
	if err := os.WriteFile(readPath, []byte("physical read boundary"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	turn := make(chan error, 1)
	go func() {
		turn <- m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "create a team then read the physical fixture"})}))
	}()
	select {
	case <-parked:
	case err := <-turn:
		t.Fatal("actual turn did not park", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		// Source reads USER_TYPE at attachment time; its private fixture has the
		// same runtime change after external startup. No startup capability is waived.
		m.options.Native.Environment["USER_TYPE"] = "ant"
		var err error
		m.teamLeader.mailbox, err = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	message := objMain(rawMain(map[string]any{"from": "worker", "text": "ACTUAL_BUSY_PEER_ARTIFACT", "timestamp": "t", "color": "blue", "summary": "actual busy summary"}))
	if err = m.teamLeader.mailbox.Write("team-lead", "actual-interactive-midturn", message); err != nil {
		t.Fatal(err)
	}
	inbox, err := m.PollInteractiveLeaderInbox(ctx)
	if err != nil || len(inbox) != 1 || inbox[0].String("status") != "pending" {
		t.Fatal("busy original owner poll did not admit queue", inbox, err)
	}
	close(release)
	select {
	case err = <-turn:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("actual turn never resumed", ctx.Err())
	}
	active, err := m.RunLeaderInboxCycle(ctx)
	if err != nil || active || posts.Load() != 3 {
		t.Fatal("processed messages resubmitted at idle", active, err, posts.Load())
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if len(m.Controller.AppState().Inbox) != 0 {
			t.Error("processed messages not cleaned")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
