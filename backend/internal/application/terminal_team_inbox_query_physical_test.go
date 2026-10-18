package application

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSessionTeamsPanelIdleNotificationStartsSameHostWithoutUnmount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var posts atomic.Int32
	incoming := make(chan string, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch count := posts.Add(1); count {
		case 1:
			response(w, "panel-inbox-create", "TeamCreate", map[string]any{"team_name": "actual-panel-inbox"}, "")
		case 2:
			response(w, "panel-inbox-ready", "", nil, "ACTUAL_TEAM_CREATED")
		case 3:
			if !strings.Contains(string(body), "idle_notification") || !strings.Contains(string(body), "teammate-message") {
				t.Error("actual queued idle notification missing from next POST", string(body))
			}
			incoming <- string(body)
			response(w, "panel-inbox-notification", "", nil, "ACTUAL_IDLE_NOTIFICATION_CONSUMED")
		default:
			t.Error("duplicate actual inbox query", count)
			http.Error(w, "extra query", 500)
		}
	}))
	defer peer.Close()
	options := nativeSessionOptions(t, peer.URL)
	options.Interactive = true
	options.Arguments.PermissionMode = "bypassPermissions"
	options.Arguments.TeammateMode = "tmux"
	options.Arguments.Tools = []string{"TeamCreate,TeamDelete,Read,SendMessage"}
	for _, env := range []map[string]string{options.Environment, options.Native.Environment} {
		env["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	}
	config := options.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	if err = session.Submit(ctx, tui.Input{Text: "Create the actual private team", Mode: "prompt"}, nil); err != nil {
		t.Fatal(err)
	}
	frame, remove, err := session.SubscribeTerminalCachedTeamsFrames(ctx, nil)
	if err != nil || frame == nil || len(frame.InitialTeams()) != 1 {
		t.Fatal("actual cached Footer frame", frame, err)
	}
	defer remove()
	ready := make(chan tui.LocalPanel, 1)
	emit := func(event tui.Event) error {
		if event.Kind == tui.LocalPanelRefreshed && event.LocalPanel != nil && !strings.Contains(string(event.LocalPanel.Presentation), `"loading":true`) {
			select {
			case ready <- *event.LocalPanel:
			default:
			}
		}
		return nil
	}
	if err = session.OpenTerminalTeams(ctx, tui.TerminalTeamsRequest{ID: "actual-source-inbox-teams", InitialTeams: frame.InitialTeams(), InitialFrame: frame}, emit); err != nil {
		t.Fatal(err)
	}
	session.SetTerminalLocalUIActive(true)
	var panel tui.LocalPanel
	select {
	case panel = <-ready:
	case <-ctx.Done():
		t.Fatal("actual Teams mounted snapshot", ctx.Err())
	}
	store, err := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: store})
	if err != nil {
		t.Fatal(err)
	}
	envelope := obj(raw(map[string]any{"from": "worker", "text": `{"type":"idle_notification","from":"worker","timestamp":"2026-10-14T00:00:00.000Z","idleReason":"available"}`, "timestamp": "2026-10-14T00:00:00.000Z", "color": "blue"}))
	if err = mailbox.Write("team-lead", "actual-panel-inbox", envelope); err != nil {
		t.Fatal(err)
	}
	select {
	case <-incoming:
		session.mu.Lock()
		active := session.terminalPanel != nil && session.terminalPanel.props.ID == panel.ID
		session.mu.Unlock()
		if !active {
			t.Error("inbox query unmounted actual Teams local JSX")
		}
	case <-time.After(3 * time.Second):
		t.Errorf("Source submits idle notification with Teams local JSX still mounted, native actual Host made %d POSTs", posts.Load())
	}
	if err = session.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "close"}, emit); err != nil {
		t.Fatal(err)
	}
	session.SetTerminalLocalUIActive(false)
	// Observe the same original queued message after close as well, so an admission
	// failure is distinct from a missing mailbox producer or lost model callback.
	if posts.Load() != 3 {
		select {
		case <-incoming:
		case <-ctx.Done():
			t.Fatal("actual inbox was lost", ctx.Err())
		}
	}
	if posts.Load() != 3 {
		t.Fatal("actual model POST cardinality", posts.Load())
	}
	if err = session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
