package application

import (
	"context"
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

func TestActualInteractiveTerminalBusyInboxThenIdleSameHostFollowUpWritesArtifactAndJoins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var posts atomic.Int32
	parked, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	target := ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch posts.Add(1) {
		case 1:
			response(w, "interactive-create", "TeamCreate", map[string]any{"team_name": "actual-terminal-inbox"}, "")
		case 2:
			close(parked)
			select {
			case <-r.Context().Done():
				return
			case <-release:
			}
			response(w, "interactive-ready", "", nil, "ACTUAL_FIRST_TURN_READY")
		case 3:
			if !strings.Contains(string(body), "ACTUAL_TERMINAL_PEER_RESULT") || !strings.Contains(string(body), `summary=\"physical peer summary\"`) {
				t.Error("idle pending input missing", string(body))
			}
			response(w, "interactive-idle-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_TERMINAL_INBOX_ARTIFACT"}, "")
		case 4:
			response(w, "interactive-idle-done", "", nil, "ACTUAL_IDLE_TEAM_RESULT_SHOWN")
		default:
			t.Error("duplicate idle mailbox turn", posts.Load())
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.ForegroundShellProgress = true
	o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Read,Write,SendMessage"}
	o.Arguments.PermissionMode = "bypassPermissions"
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(o.WorkDir, "terminal-inbox-artifact.txt")
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := &teammateHarnessOutput{}
	done := make(chan error, 1)
	go func() {
		done <- tui.Run(ctx, s, tui.Options{Input: reader, Output: output, InitialPrompt: "create a team and wait for the peer", Title: "actual inbox fixture", Width: 100, Height: 35, Environment: []string{"TERM=xterm-256color"}})
	}()
	wait := func(predicate func() bool, label string) {
		t.Helper()
		for !predicate() {
			select {
			case err := <-done:
				t.Fatal("terminal returned before "+label, err, output.Text())
			case <-ctx.Done():
				t.Fatal(label, ctx.Err(), output.Text())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	wait(func() bool {
		select {
		case <-parked:
			return true
		default:
			return false
		}
	}, "parked model")
	store, err := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: store})
	if err != nil {
		t.Fatal(err)
	}
	if err = mailbox.Write("team-lead", "actual-terminal-inbox", obj(raw(protocol.NewJSONObject(field("from", "worker"), field("text", "ACTUAL_TERMINAL_PEER_RESULT"), field("timestamp", "t"), field("summary", "physical peer summary"), field("color", "blue"))))); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return strings.Contains(output.Text(), "@worker · physical peer summary") }, "busy inbox footer before model completes")
	if posts.Load() != 2 {
		t.Fatal("poll started a second ASK while busy", posts.Load())
	}
	close(release)
	wait(func() bool {
		data, _ := os.ReadFile(target)
		return string(data) == "ACTUAL_TERMINAL_INBOX_ARTIFACT" && strings.Contains(output.Text(), "ACTUAL_IDLE_TEAM_RESULT_SHOWN")
	}, "idle same-host artifact and autonomous transcript")
	cancel()
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal and inbox lifecycle did not join")
	}
	if posts.Load() != 4 {
		t.Fatal("close or processed inbox queried again", posts.Load())
	}
	if s.leaderInbox == nil {
		t.Fatal("real interactive lifecycle never started")
	}
	select {
	case <-s.leaderInbox.done:
	default:
		t.Fatal("interactive poller escaped terminal close")
	}
}
