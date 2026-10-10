package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualNativeLeaderInboxQueuesOnlyChatIntoSameHarnessAndResolvesPermissionPhysically(t *testing.T) {
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if posts.Add(1) != 1 {
			t.Error("unexpected parent model request")
			http.Error(w, "unexpected", 500)
			return
		}
		if !strings.Contains(string(body), "ACTUAL_LEADER_CHILD_ARTIFACT") || strings.Contains(string(body), "permission_request") || strings.Contains(string(body), "private permission input") {
			t.Error("leader context leaked control or lost chat", string(body))
		}
		emitPlanFixture(w, 1, "", "ACTUAL_LEADER_RECEIVED")
	}))
	defer peer.Close()
	var mu sync.Mutex
	asked := []queryloop.JSONToolCallContext{}
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1" }, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Read,SendMessage"}
		o.ResolvePermission = func(_ context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
			if tool.Name != "Write" || input.String("content") != "private permission input" || call.PermDecision == nil || call.PermDecision.DisplayName == nil {
				t.Error("original host permission descriptor missing", tool.Name, input, call)
			}
			mu.Lock()
			asked = append(asked, call)
			mu.Unlock()
			return queryloop.ToolPermission{Behavior: "allow", UpdatedInput: input, UpdatedPermissions: []byte(`[{"type":"addRules","destination":"session","rules":[{"toolName":"Write"}],"behavior":"allow"}]`)}, nil
		}
	})
	callActualTeamTool(t, m, "TeamCreate", `{"team_name":"actual-leader-inbox"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		snapshot := m.teamState.snapshot.Clone()
		roster, _ := snapshot.Object("teammates")
		roster = roster.Clone()
		roster.Set("worker@actual-leader-inbox", rawMain(map[string]string{"name": "worker", "color": "blue"}))
		snapshot.Set("teammates", rawMain(roster))
		m.teamState.snapshot = snapshot
		var err error
		m.teamLeader.mailbox, err = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"ACTUAL_LEADER_CHILD_ARTIFACT", `{"type":"permission_request","request_id":"actual-permission","agent_id":"worker","tool_name":"Write","tool_use_id":"actual-write","description":"physical file","input":{"content":"private permission input"},"permission_suggestions":[]}`} {
		if err := m.teamLeader.mailbox.Write("team-lead", "actual-leader-inbox", objMain(rawMain(map[string]string{"from": "worker", "text": text, "timestamp": "2026-10-07T00:00:00.000Z"}))); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		poll, err := m.nativeLeaderInboxOnOwner(ctx, scope, false)
		if err != nil {
			return err
		}
		if !poll {
			t.Error("actual leader inbox did not schedule queue")
		}
		ran, err := m.Host.RunDequeued(scope, ctx)
		if !ran {
			t.Error("same main Harness did not consume leader prompt")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var responses []protocol.JSONObject
	for ctx.Err() == nil {
		_ = json.Unmarshal(m.teamLeader.mailbox.Read("worker", "actual-leader-inbox"), &responses)
		if len(responses) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(responses) != 1 {
		t.Fatal("physical worker resolution missing", responses, ctx.Err())
	}
	resolution := objMain([]byte(responses[0].String("text")))
	payload, _ := resolution.Object("response")
	if resolution.String("subtype") != "success" || !strings.Contains(string(payload.Get("permission_updates")), "session") {
		t.Fatal("original permission response missing", resolution)
	}
	mu.Lock()
	count := len(asked)
	mu.Unlock()
	if count != 1 || posts.Load() != 1 {
		t.Fatal("original host/model call count", count, posts.Load())
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal("actual leader lifecycle did not drain permission future", err)
	}
}
