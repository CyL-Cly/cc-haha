package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type teammateHarnessOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func writerCloseForTeammateTest(s *Session) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.Close(ctx)
}

func (b *teammateHarnessOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(data)
}
func (b *teammateHarnessOutput) Text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func TestActualInteractiveTeammateTerminalCancelsPhysicalWorkSendsFreshUserApprovesSamePendingAndKills(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var parents, children atomic.Int32
	target, active := "", ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("# Agent Teammate Communication")) {
			switch children.Add(1) {
			case 1:
				response(w, "tui-child-shell", "Bash", map[string]any{"command": "printf ACTIVE > " + active + "; sleep 30", "description": "actual cancellable teammate shell"}, "")
			case 2:
				if !bytes.Contains(body, []byte("ACTUAL_TUI_PEER_CONTINUATION")) || bytes.Contains(body, []byte("[Request interrupted by user]")) {
					t.Error("actual fresh child continuation/history filter invalid")
				}
				response(w, "tui-child-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_INTERACTIVE_TEAM_ARTIFACT"}, "")
			case 3:
				response(w, "tui-child-read", "Read", map[string]any{"file_path": target}, "")
			case 4:
				response(w, "tui-child-final", "", nil, "ACTUAL_TEAMMATE_TUI_CONTINUED")
			default:
				t.Error("extra child model request", children.Load())
				http.Error(w, "unexpected", 500)
			}
			return
		}
		// The migrated leader poller now consumes actual worker idle notices.
		// These automatic turns remain separate from user input sent to a peer.
		if bytes.Contains(body, []byte("<teammate-message")) {
			if bytes.Contains(body, []byte("ACTUAL_TUI_PEER_CONTINUATION")) {
				t.Error("peer user input leaked into leader idle turn")
			}
			response(w, "tui-leader-idle", "", nil, "ACTUAL_LEADER_IDLE_UPDATE")
			return
		}
		switch parents.Add(1) {
		case 1:
			response(w, "tui-team-create", "TeamCreate", map[string]any{"team_name": "actual-interactive-team"}, "")
		case 2:
			response(w, "tui-team-spawn", "Agent", map[string]any{"name": "worker", "prompt": "run real work then wait", "description": "actual interactive worker"}, "")
		case 3:
			response(w, "tui-parent-idle", "", nil, "ACTUAL_PARENT_TUI_READY")
		default:
			t.Error("teammate input reached leader model", parents.Load())
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.ForegroundShellProgress = true
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,Bash,Read,Write,SendMessage"}
	o.Native.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	o.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	target = filepath.Join(o.WorkDir, "interactive-peer.txt")
	active = filepath.Join(o.WorkDir, "interactive-active.txt")
	s, e := NewSession(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { cancel(); _ = writerCloseForTeammateTest(s) }()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := &teammateHarnessOutput{}
	done := make(chan error, 1)
	go func() {
		done <- tui.Run(ctx, s, tui.Options{Input: reader, Output: output, InitialPrompt: "create a team and spawn actual work", Title: "actual private team", Width: 100, Height: 35, Environment: []string{"TERM=xterm-256color"}})
	}()
	wait := func(predicate func() bool, label string) {
		t.Helper()
		for !predicate() {
			select {
			case err := <-done:
				t.Fatal("terminal returned before "+label, err, output.Text())
			case <-ctx.Done():
				t.Fatal(label, "not reached", output.Text())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	send := func(data string) {
		t.Helper()
		if _, e := writer.Write([]byte(data)); e != nil {
			t.Fatal(e)
		}
	}
	wait(func() bool { return strings.Contains(output.Text(), "Allow Bash?") }, "actual child parent permission")
	send("\r")
	wait(func() bool {
		data, _ := os.ReadFile(active)
		return string(data) == "ACTIVE" && strings.Contains(output.Text(), "ACTUAL_PARENT_TUI_READY")
	}, "actual physical child work and parent result")
	send("\x1b[1;2B\x1b[1;2B\r")
	wait(func() bool { return strings.Contains(output.Text(), "@worker") }, "actual teammate view")
	send("\x1b")
	wait(func() bool { return strings.Contains(output.Text(), "[Request interrupted by user]") }, "work-only interruption")
	if children.Load() != 1 {
		t.Fatal("Escape queried a model", children.Load())
	}
	send("ACTUAL_TUI_PEER_CONTINUATION\r")
	wait(func() bool { return strings.Contains(output.Text(), "Allow Write?") }, "idle worker original pending permission")
	if len(s.PendingPermissions()) != 1 {
		t.Fatal("actual parent pending registry missing")
	}
	send("\r")
	wait(func() bool {
		data, _ := os.ReadFile(target)
		return string(data) == "ACTUAL_INTERACTIVE_TEAM_ARTIFACT" && strings.Contains(output.Text(), "ACTUAL_TEAMMATE_TUI_CONTINUED")
	}, "real approved child artifact/transcript")
	if parents.Load() != 3 || children.Load() != 4 || len(s.PendingPermissions()) != 0 {
		t.Fatal("wrong actual harness/permission lifecycle", parents.Load(), children.Load())
	}
	// Shift+Down expands/parks on leader after leaving a view, then selects the
	// one running teammate. k invokes the original dedicated kill producer.
	send("\x1b[1;2B\x1b[1;2B\x1b[1;2B")
	send("k")
	wait(func() bool {
		return strings.Contains(output.Text(), "Leader") && !strings.Contains(output.Text()[max(0, len(output.Text())-200):], "@worker · working") && !strings.Contains(output.Text()[max(0, len(output.Text())-200):], "@worker · idle")
	}, "terminal kill update")
	cancel()
	select {
	case e = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal/actual worker did not join")
	}
	if parents.Load() != 3 || children.Load() != 4 {
		t.Fatal("closing started a model request")
	}
}
