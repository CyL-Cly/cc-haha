package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
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

func TestActualDurableLeaderIdlePumpAcceptsNextHumanUsesSamePendingPermissionAndCloseJoinsChild(t *testing.T) {
	var parentPosts, childPosts atomic.Int32
	childStarted := make(chan struct{})
	releaseChild := make(chan struct{})
	defer close(releaseChild)
	target := ""
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "# Agent Teammate Communication") {
			if childPosts.Add(1) == 1 {
				close(childStarted)
			}
			select {
			case <-r.Context().Done():
			case <-releaseChild:
			}
			return
		}
		switch parentPosts.Add(1) {
		case 1:
			response(w, "durable-create", "TeamCreate", map[string]any{"team_name": "actual-durable-team"}, "")
		case 2:
			response(w, "durable-spawn", "Agent", map[string]any{"name": "worker", "prompt": "wait while the leader continues", "description": "actual live worker"}, "")
		case 3:
			response(w, "durable-ready", "", nil, "LEADER_USER_TURN_READY")
		case 4:
			if !strings.Contains(string(body), "NEXT_HUMAN_WHILE_TEAM_ACTIVE") {
				t.Error("next human lost")
			}
			response(w, "next-human", "", nil, "SECOND_HUMAN_OK")
		case 5:
			if !strings.Contains(string(body), "REAL_IDLE_PEER_RESULT") {
				t.Error("idle mailbox not in same history")
			}
			response(w, "idle-write", "Write", map[string]any{"file_path": target, "content": "DURABLE_IDLE_PHYSICAL_ARTIFACT"}, "")
		case 6:
			response(w, "idle-finished", "", nil, "DURABLE_IDLE_FOLLOW_UP_COMPLETE")
		default:
			t.Error("unexpected parent POST", parentPosts.Load())
			http.Error(w, "unexpected", 500)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,Read,Write,SendMessage"}
	o.Arguments.PermissionMode = "default"
	o.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	// nativeSessionOptions has already captured the launch environment. A real
	// process sees the feature flag before NewProcessNativeBoundary snapshots it.
	o.Native.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	if err := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(o.WorkDir, "durable-leader.txt")
	sdk := make(chan json.RawMessage, 1000)
	o.OnSDK = func(data json.RawMessage) error { sdk <- append(json.RawMessage(nil), data...); return nil }
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	permissions := make(chan tui.PermissionRequest, 10)
	emit := func(event tui.Event) error {
		if event.Kind == tui.PermissionAsked && event.Permission != nil {
			permissions <- *event.Permission
		}
		return nil
	}
	turn := make(chan error, 1)
	go func() {
		turn <- s.Submit(ctx, tui.Input{TurnID: "first", Text: "create a team and leave its worker running"}, emit)
	}()
	select {
	case err = <-turn:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("first turn held the leader turn mutex while idle")
	}
	select {
	case <-childStarted:
	case <-ctx.Done():
		t.Fatal("actual independent child did not start")
	}
	if err = s.Submit(ctx, tui.Input{TurnID: "second", Text: "NEXT_HUMAN_WHILE_TEAM_ACTIVE"}, emit); err != nil {
		t.Fatal("idle job blocked next human", err)
	}
	store, err := taskstore.New(taskstore.Options{ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], PID: os.Getpid(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], TaskStore: store})
	if err != nil {
		t.Fatal(err)
	}
	if err = mailbox.Write("team-lead", "actual-durable-team", protocol.NewJSONObject(field("from", "worker"), field("text", "REAL_IDLE_PEER_RESULT"), field("timestamp", "2026-10-07T00:00:00.000Z"))); err != nil {
		t.Fatal(err)
	}
	var permission tui.PermissionRequest
	select {
	case permission = <-permissions:
	case <-ctx.Done():
		t.Fatal("original parent permission UI was not invoked by idle model")
	}
	if permission.ToolName != "Write" || obj(permission.Payload).String("agentId") != "" {
		t.Fatal("idle leader used a child permission registry", permission)
	}
	if err = s.RespondPermission(ctx, permission.ID, tui.PermissionDecision{Allowed: true}); err != nil {
		t.Fatal("same pending permission not consumed", err)
	}
	for ctx.Err() == nil {
		if data, err := os.ReadFile(target); err == nil && string(data) == "DURABLE_IDLE_PHYSICAL_ARTIFACT" && parentPosts.Load() == 6 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("actual idle write/follow-up missing", parentPosts.Load())
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal("owned idle job or actual child did not join", err)
	}
	s.mu.Lock()
	job := s.leaderInbox
	s.mu.Unlock()
	if job == nil {
		t.Fatal("actual idle job missing")
	}
	select {
	case <-job.done:
	default:
		t.Fatal("idle job escaped Session.Close")
	}
	if childPosts.Load() != 1 {
		t.Fatal("closing generated an extra child model request", childPosts.Load())
	}
	if len(s.PendingPermissions()) != 0 {
		t.Fatal("idle permission lifecycle remained pending")
	}
}
