package application

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillwatcher"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"golang.org/x/net/websocket"
)

type commandMenuOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *commandMenuOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *commandMenuOutput) text() string { b.mu.Lock(); defer b.mu.Unlock(); return b.Buffer.String() }

func liveCommandOptions(t *testing.T) (Options, *computeruse.Service, *atomic.Int64) {
	t.Helper()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("idle command change started a model request")
		http.Error(w, "no model allowed", 500)
	}))
	t.Cleanup(model.Close)
	o := nativeSessionOptions(t, model.URL)
	disabled := false
	o.Arguments.ComputerUse = &disabled
	now := new(atomic.Int64)
	now.Store(5000)
	o.Native.NowMillis = func() float64 { return float64(now.Load()) }
	o.Native.Temp = filepath.Join(o.Environment["HOME"], "tmp")
	_ = os.MkdirAll(o.Native.Temp, 0700)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(filepath.Join(config, "cc-haha"), 0700)
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false}`), 0600)
	_ = os.WriteFile(filepath.Join(config, "cc-haha", "computer-use-config.json"), []byte(`{"enabled":false,"future":{"keep":true}}`), 0600)
	service, e := computeruse.New(computeruse.Options{HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, TempDir: o.Native.Temp, Environment: o.Environment, Platform: "darwin"})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	o.ComputerUse = &clibootstrap.NativeComputerUseOptions{Service: service, SkillWatchTiming: &skillwatcher.Timing{Interval: 10 * time.Millisecond, Stability: 50 * time.Millisecond, StabilityPoll: 10 * time.Millisecond, Debounce: 30 * time.Millisecond, Atomic: 10 * time.Millisecond}}
	return o, service, now
}
func TestActualIdleSessionFileSignalUpdatesRunningTerminalMenu(t *testing.T) {
	o, service, now := liveCommandOptions(t)
	// Construct the real mounted REPL profile before binding terminal consumers.
	// Desktop callers below retain their separate noninteractive session profile.
	o.Interactive = true
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output commandMenuOutput
	done := make(chan error, 1)
	go func() {
		done <- tui.Run(ctx, s, tui.Options{Input: reader, Output: &output, Environment: []string{"TERM=dumb", "TERM_PROGRAM=Apple_Terminal"}, Width: 120, Height: 24})
	}()
	inputWritten := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("/computer"))
		inputWritten <- err
	}()
	select {
	case err := <-inputWritten:
		if err != nil {
			t.Fatal("actual terminal input failed", err)
		}
	case err := <-done:
		t.Fatal("actual terminal exited before reading command input", err)
	case <-time.After(3 * time.Second):
		t.Fatal("actual terminal did not read command input")
	}
	now.Add(4000)
	if e := service.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true}`)); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(output.text(), "computer-use") {
		if time.Now().After(deadline) {
			t.Fatal("actual terminal menu stayed stale", output.text())
		}
		time.Sleep(10 * time.Millisecond)
	}
	initial, _ := s.SubscribeCommands(nil)
	found := false
	for _, c := range initial {
		found = found || c.Name == "computer-use"
	}
	if !found {
		t.Fatal("visible terminal and shared session snapshot diverged")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("actual terminal did not drain session")
	}
}
func TestActualDesktopHTTPConfigWritePushesIdleWebSocketSlashMenu(t *testing.T) {
	o, service, now := liveCommandOptions(t)
	opened := make(chan *DesktopSession, 1)
	factory := DesktopFactory(o)
	s, e := server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, ComputerUse: service, Factory: func(ctx context.Context, opts server.SessionOptions, emit server.Emit) (server.LiveSession, error) {
		live, e := factory(ctx, opts, emit)
		if e == nil {
			opened <- live.(*DesktopSession)
		}
		return live, e
	}})
	if e != nil {
		t.Fatal(e)
	}
	host := httptest.NewServer(s)
	defer host.Close()
	defer s.Close(context.Background())
	req := func(method, path string, data string) []byte {
		t.Helper()
		request, _ := http.NewRequest(method, host.URL+path, strings.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		response, e := host.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Fatalf("%s %s status=%d %s", method, path, response.StatusCode, body)
		}
		return body
	}
	id := obj(req("POST", "/api/sessions", string(raw(map[string]any{"workDir": o.WorkDir})))).String("sessionId")
	ws, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if e != nil {
		t.Fatal(e)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(5 * time.Second))
	if e := websocket.Message.Send(ws, `{"type":"prewarm_session"}`); e != nil {
		t.Fatal(e)
	}
	var d *DesktopSession
	select {
	case d = <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("real shared desktop host not prewarmed")
	}
	for _, enabled := range []bool{true, false} {
		now.Add(4000)
		req("PUT", "/api/computer-use/authorized-apps", string(raw(map[string]bool{"enabled": enabled})))
		for count := 0; count < 100; count++ {
			var wire string
			if e := websocket.Message.Receive(ws, &wire); e != nil {
				t.Fatal(e)
			}
			frame := obj([]byte(wire))
			if frame.String("type") == "error" {
				t.Fatal(wire)
			}
			if frame.String("type") != "system_notification" || frame.String("subtype") != "slash_commands" {
				continue
			}
			var commands []struct {
				Name string `json:"name"`
			}
			if e := json.Unmarshal(frame.Get("data"), &commands); e != nil {
				t.Fatal(e, wire)
			}
			present := false
			for _, c := range commands {
				present = present || c.Name == "computer-use"
			}
			if present != enabled {
				t.Fatal("idle websocket menu stale", wire)
			}
			current, _ := d.engine.SubscribeCommands(nil)
			present = false
			for _, c := range current {
				present = present || c.Name == "computer-use"
			}
			if present != enabled {
				t.Fatal("desktop and shared host snapshot diverged")
			}
			break
		}
	}
	config, e := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", "computer-use-config.json"))
	if e != nil || obj(config).Get("future") == nil {
		t.Fatal("HTTP write lost old future config on disk", string(config), e)
	}
}
