//go:build !windows

package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func newTerminalTeamsPhysicalSession(t *testing.T, url string) (*Session, *teamstore.Config, string) {
	t.Helper()
	o := nativeSessionOptions(t, url)
	o.Interactive = true
	o.Arguments.TeammateMode = "tmux"
	o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Read"}
	home, configDir := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(home, "pane-release")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	body := "#!/usr/bin/python3\nimport os,sys,json\nwith open(os.environ['CC_HAHA_TEAM_PANE_RECEIPT'],'a') as f:f.write(json.dumps({'pid':os.getpid(),'args':sys.argv[1:]})+'\\n')\nif 'kill-pane' in sys.argv or 'select-pane' in sys.argv:\n with open(os.environ['CC_HAHA_TEAM_PANE_RELEASE']) as f:f.read()\n open(os.path.join(os.environ['HOME'],'forbidden-late-pane'),'w').write('late')\nprint('tmux 3.5')\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["PATH"] = bin + ":/usr/bin:/bin"
		env["TMUX"] = filepath.Join(home, "private-socket") + ",1,0"
		env["CC_HAHA_TEAM_PANE_RECEIPT"] = filepath.Join(home, "pane-requests.jsonl")
		env["CC_HAHA_TEAM_PANE_RELEASE"] = fifo
		env["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
		env["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
		env["NO_PROXY"] = "127.0.0.1,localhost"
	}
	seedTerminalReleaseWarmFixture(t, o)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		fd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0600)
		if err == nil {
			_, _ = syscall.Write(fd, []byte("release"))
			_ = syscall.Close(fd)
		}
		_ = s.Close(context.Background())
	})
	if err := s.Submit(context.Background(), tui.Input{Text: "TEAMS_CREATE_ACTUAL_TOOL"}, nil); err != nil {
		t.Fatal(err)
	}
	tasks, err := taskstore.New(taskstore.Options{ConfigDir: configDir, PID: os.Getpid(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	config, err := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: configDir, TaskStore: tasks, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	file := config.Read("application-teams")
	if file == nil {
		t.Fatal("actual TeamCreate physical config missing")
	}
	var members []protocol.JSONObject
	_ = json.Unmarshal(file.Get("members"), &members)
	members = append(members, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw("held")}, protocol.JSONField{Name: "agentId", Value: raw("held@application-teams")}, protocol.JSONField{Name: "tmuxPaneId", Value: raw("%1")}, protocol.JSONField{Name: "backendType", Value: raw("tmux")}, protocol.JSONField{Name: "isActive", Value: raw(false)}))
	file.Set("members", raw(members))
	if err = config.Write("application-teams", *file); err != nil {
		t.Fatal(err)
	}
	if err = s.process.SetTeamRuntimeSnapshot(context.Background(), "application-teams", file.Get("createdAt")); err != nil {
		t.Fatal(err)
	}
	return s, config, home
}
func awaitTerminalTeamsPanel(t *testing.T, events <-chan tui.Event, id string) tui.LocalPanel {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Kind == tui.LocalPanelRefreshed && event.LocalPanel != nil && event.LocalPanel.ID == id && len(event.LocalPanel.Rows) == 1 {
				return *event.LocalPanel
			}
		case <-timer.C:
			t.Fatal("actual Teams member file projection missing")
		}
	}
}

func terminalTeamsPhysicalResult(panel tui.LocalPanel, action string, index int) tui.LocalPanelResult {
	var presentation protocol.JSONObject
	_ = presentation.UnmarshalJSON(panel.Presentation)
	result := tui.LocalPanelResult{ID: panel.ID, Action: action, TeamsFrame: panel.TeamsFrame, TeamsIndex: index, TeamsLevel: presentation.String("level")}
	if index >= 0 && index < len(panel.Rows) {
		result.Row = panel.Rows[index].ID
	}
	return result
}
func TestActualTeamsMountedRetireAndGlobalCloseJoinsHeldPaneAndModel(t *testing.T) {
	var calls atomic.Int32
	modelStarted, modelClosed := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "fake-only-loopback" {
			t.Error("actual model consumer boundary", r.Method, r.URL, r.Header.Get("x-api-key"))
			return
		}
		switch calls.Add(1) {
		case 1:
			if !strings.Contains(string(body), "TEAMS_CREATE_ACTUAL_TOOL") {
				t.Error(string(body))
			}
			response(w, "team", "TeamCreate", map[string]any{"team_name": "application-teams"}, "")
		case 2:
			response(w, "ready", "", nil, "TEAM_READY")
		case 3:
			if !strings.Contains(string(body), "TEAMS_HELD_MODEL") {
				t.Error(string(body))
			}
			close(modelStarted)
			<-r.Context().Done()
			close(modelClosed)
		default:
			t.Error("unowned model request")
		}
	}))
	t.Cleanup(server.Close)
	s, config, home := newTerminalTeamsPhysicalSession(t, server.URL)
	initial, remove, err := s.SubscribeTerminalCachedTeams(context.Background(), nil)
	if err != nil || len(initial) != 1 {
		t.Fatal(initial, err)
	}
	defer remove()
	s.RetireTerminalTeams("closed-before-open")
	if err := s.OpenTerminalTeams(context.Background(), tui.TerminalTeamsRequest{ID: "closed-before-open", InitialTeams: initial}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("retired source component admitted another native mount", err)
	}
	events := make(chan tui.Event, 64)
	var mu sync.Mutex
	callbacks := []tui.Event{}
	emit := func(event tui.Event) error {
		mu.Lock()
		callbacks = append(callbacks, event)
		mu.Unlock()
		events <- event
		return nil
	}
	if err := s.OpenTerminalTeams(context.Background(), tui.TerminalTeamsRequest{ID: "active-teams", InitialTeams: initial}, emit); err != nil {
		t.Fatal(err)
	}
	panel := awaitTerminalTeamsPanel(t, events, "active-teams")
	if err := s.ApplyLocalPanel(context.Background(), terminalTeamsPhysicalResult(panel, "teams-kill", 0), emit); err != nil {
		t.Fatal(err)
	}
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for pid == 0 {
		data, _ := os.ReadFile(filepath.Join(home, "pane-requests.jsonl"))
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var row struct {
				PID  int
				Args []string
			}
			if json.Unmarshal([]byte(line), &row) == nil && len(row.Args) > 0 && row.Args[0] == "kill-pane" {
				pid = row.PID
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("actual pane kill did not start")
		}
		time.Sleep(time.Millisecond)
	}
	s.RetireTerminalTeams(panel.ID)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatal("Source initiated action must remain live after component unmount", err)
	}
	modelDone := make(chan error, 1)
	go func() { modelDone <- s.Submit(context.Background(), tui.Input{Text: "TEAMS_HELD_MODEL"}, nil) }()
	select {
	case <-modelStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("held owned pane blocked next actual model ASK")
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close(context.Background()) }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		stack := make([]byte, 2<<20)
		stack = stack[:runtime.Stack(stack, true)]
		t.Logf("actual held close stacks:\n%s", stack)
		t.Fatal("actual global Close did not cancel/join pane and model")
	}
	select {
	case <-modelClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("model physical connection still open")
	}
	select {
	case <-modelDone:
	case <-time.After(3 * time.Second):
		t.Fatal("held model did not join")
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatal("actual pane process not reaped", pid, err)
	}
	data, err := os.ReadFile(filepath.Join(home, "pane-requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	kills := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var row struct {
			PID  int
			Args []string
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal("actual native cleanup receipt", err, line)
		}
		if err := syscall.Kill(row.PID, 0); err != syscall.ESRCH {
			t.Fatal("global Close left a native pane primitive alive", row.PID, row.Args, err)
		}
		if len(row.Args) > 0 && row.Args[0] == "kill-pane" {
			kills++
		}
	}
	if kills != 2 {
		t.Fatal("owned dialog kill and original orphan cleanup did not both execute", kills, string(data))
	}
	if _, err := os.Stat(filepath.Join(home, "forbidden-late-pane")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("late physical pane side effect", err)
	}
	file := config.Read("application-teams")
	if file == nil || !strings.Contains(string(file.Get("members")), "held@application-teams") {
		t.Fatal("cancelled pane operation performed late member deletion", file)
	}
	mu.Lock()
	count := len(callbacks)
	mu.Unlock()
	if count != 1 {
		t.Fatal("retired component emitted late UI update", count)
	}
	if calls.Load() != 3 {
		t.Fatal("unexpected model count", calls.Load())
	}
	if err := s.OpenTerminalTeams(context.Background(), tui.TerminalTeamsRequest{ID: "after-global-close", InitialTeams: initial}, emit); !errors.Is(err, context.Canceled) {
		t.Fatal("closed lifetime mounted Teams", err)
	}
	t.Log("actual mounted pane and held model: retired component keeps accepted action; global Close cancels and joins model plus dialog/orphan pane primitives, every observed PID ESRCH, no late file/member/UI write")
}
