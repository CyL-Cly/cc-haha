package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualInProcessChildOwnMidTurnMailboxAttachmentLeavesStructuredAndLeaderPending(t *testing.T) {
	parked, release := make(chan struct{}), make(chan struct{})
	var released atomic.Bool
	defer func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}()
	var requests atomic.Int32
	path := ""
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch requests.Add(1) {
		case 1:
			close(parked)
			select {
			case <-r.Context().Done():
				return
			case <-release:
			}
			emitNativeTaskTool(w, "own-inbox-read", "Read", map[string]any{"file_path": path})
		case 2:
			if strings.Count(string(body), "ACTUAL_INPROCESS_OWN_MIDTURN_MAIL") != 1 || strings.Contains(string(body), "ACTUAL_LEADER_PENDING_PRIVATE") || strings.Contains(string(body), "permission_request") {
				t.Error("inprocess attachment leakage/missing", string(body))
			}
			emitPlanFixture(w, 2, "", "actual inprocess midturn delivered")
		default:
			t.Error("extra child request", requests.Load())
			http.Error(w, "unexpected", 500)
		}
	}))
	defer provider.Close()
	parent := backgroundAgentMainWithOptions(t, provider.URL, func(n *NativeBoundary) { n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1" }, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.Tools = []string{"Read,Write,SendMessage,TeamCreate,TeamDelete"}
	})
	callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"actual-own-inbox"}`)
	path = filepath.Join(parent.workingDirectory(), "inprocess-own-read.txt")
	_ = os.WriteFile(path, []byte("actual source child read"), 0600)
	identity := sessioncontroller.InProcessTeammateIdentity{AgentID: "worker@actual-own-inbox", AgentName: "worker", TeamName: "actual-own-inbox", ParentSessionID: parent.Controller.SessionID(), Color: "blue"}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var prepared nativeInProcessTurnPrepared
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		prepared, err = parent.prepareNativeInProcessTurn(ctx, scope, nativeInProcessTurn{Identity: identity, Model: parent.options.Arguments.Model, PermissionMode: "default", Prompt: "actual own inbox initial"})
		if err != nil {
			return err
		}
		return parent.Controller.QueueTeamInboxOnOwner(scope, []protocol.JSONObject{objMain(rawMain(map[string]string{"from": "peer", "text": "ACTUAL_LEADER_PENDING_PRIVATE", "timestamp": "leader"}))})
	}); err != nil {
		t.Fatal(err)
	}
	defer prepared.ref.DeliveryFinished()
	child, err := NewMainPrint(prepared.options)
	if err != nil {
		t.Fatal(err)
	}
	defer child.ShutdownSession(context.Background())
	if err = child.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- child.RunTurn(ctx, objMain(rawMain(map[string]any{"type": "user", "message": map[string]string{"role": "user", "content": "actual child read fixture"}})))
	}()
	select {
	case <-parked:
	case err := <-done:
		t.Fatal("child never parked", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	receipt, err := child.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		child.options.Native.Environment["USER_TYPE"] = "ant"
		delete(child.options.Native.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: parent.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: parent.taskStore})
	if err != nil {
		t.Fatal(err)
	}
	_ = mailbox.Write("worker", identity.TeamName, objMain(rawMain(map[string]string{"from": "peer", "text": "ACTUAL_INPROCESS_OWN_MIDTURN_MAIL", "timestamp": "midturn", "summary": "own child mail", "color": "green"})))
	_ = mailbox.Write("worker", identity.TeamName, objMain(rawMain(map[string]string{"from": "team-lead", "text": `{"type":"permission_request","request_id":"must-stay-unread"}`, "timestamp": "protocol"})))
	released.Store(true)
	close(release)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("child never resumed", ctx.Err())
	}
	unread, err := mailbox.Unread("worker", identity.TeamName)
	if err != nil || len(unread) != 1 || !strings.Contains(string(unread[0]), "permission_request") {
		t.Fatal("structured producer consumed as attachment", unread, err)
	}
	sourceRows := []map[string]any{{"messages": []protocol.JSONObject{objMain(rawMain(map[string]string{"from": "peer", "text": "ACTUAL_INPROCESS_OWN_MIDTURN_MAIL", "timestamp": "midturn", "summary": "own child mail", "color": "green"})), objMain(rawMain(map[string]string{"from": "team-lead", "text": `{"type":"permission_request","request_id":"must-stay-unread"}`, "timestamp": "protocol"}))}, "pending": parent.Controller.AppState().Inbox, "inProcess": map[string]any{"agentId": identity.AgentID, "agentName": "worker", "planModeRequired": false, "parentSessionId": identity.ParentSessionID}}}
	_, source := runInteractiveInboxSource(t, nil, sourceRows)
	actual := []map[string]any{}
	if err := child.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		history, err := child.Host.OriginalHistoryOnOwner(scope)
		if err != nil {
			return err
		}
		payloads := []protocol.JSONObject{}
		normalized := []map[string]any{}
		for _, record := range history {
			if record.Type() != "attachment" {
				continue
			}
			payload := objMain(record.Record.Get("attachment"))
			if payload.String("type") != "teammate_mailbox" {
				continue
			}
			payloads = append(payloads, payload)
			users, err := attachments.Project(rawMain(payload), child.factory)
			if err != nil {
				return err
			}
			for _, user := range users {
				body, _ := user.Object("message")
				normalized = append(normalized, map[string]any{"content": body.Get("content"), "isMeta": user.Get("isMeta")})
			}
		}
		var mailboxRows []protocol.JSONObject
		_ = json.Unmarshal(mailbox.Read("worker", identity.TeamName), &mailboxRows)
		actual = append(actual, map[string]any{"attachments": payloads, "normalized": normalized, "pending": parent.Controller.AppState().Inbox, "mailbox": cleanInteractiveMessages(mailboxRows)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal(rawMain(actual), &a)
	_ = json.Unmarshal(source, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("actual Source in-process attachment/API differs\nGo=%s\nSource=%s", rawMain(actual), source)
	}
	if err = parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		pending, err := parent.Controller.LeaderPendingTeamInboxOnOwner(scope)
		if err != nil || len(pending) != 1 || pending[0].String("text") != "ACTUAL_LEADER_PENDING_PRIVATE" {
			t.Fatal("child changed parent pending inbox", pending, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
