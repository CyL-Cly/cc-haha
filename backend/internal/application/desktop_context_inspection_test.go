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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/net/websocket"
)

func TestActualDesktopInspectionReadsRunningAndCompletedSameNativeContext(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		response(w, "desktop-context-actual", "", nil, "ACTUAL_CONTEXT_SHARED_HARNESS")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Environment["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"] = `{"fixture-model":1000000}`
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Post(host.URL+"/api/sessions", "application/json", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != 201 {
		t.Fatal(res.StatusCode, err, string(data))
	}
	id := obj(data).String("sessionId")
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetDeadline(time.Now().Add(20 * time.Second))
	if err = websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "Use the same actual Agent conversation for context inspection."}))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("ordinary native request not started")
	}
	get := func() protocol.JSONObject {
		t.Helper()
		res, err := client.Get(host.URL + "/api/sessions/" + id + "/inspection?contextOnly=1")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != 200 {
			t.Fatal(res.StatusCode, err, string(data))
		}
		return obj(data)
	}
	running := get()
	contextData := obj(running.Get("context"))
	if contextData.String("model") != "fixture-model" || string(contextData.Get("maxTokens")) != "1000000" || string(obj(running.Get("errors")).Get("context")) != "null" && obj(running.Get("errors")).Get("context") != nil || calls.Load() != 1 {
		t.Fatal("actual running context missing", running, calls.Load())
	}
	close(release)
	completed := false
	for {
		var text string
		if err = websocket.Message.Receive(ws, &text); err != nil {
			t.Fatal(err)
		}
		frame := obj([]byte(text))
		if frame.String("type") == "error" {
			t.Fatal(text)
		}
		if frame.String("type") == "message_complete" {
			completed = true
		}
		if completed && frame.String("type") == "session_state" && frame.String("turnState") == "idle" {
			break
		}
	}
	var canonical string
	if err = filepath.WalkDir(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == id+".jsonl" {
			canonical = path
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	afterTurn := get()
	contextData = obj(afterTurn.Get("context"))
	usage := obj(contextData.Get("apiUsage"))
	if string(usage.Get("input_tokens")) == "0" || usage.Get("input_tokens") == nil || !bytes.Contains(contextData.Get("messageBreakdown"), []byte("assistantMessageTokens")) || calls.Load() != 1 {
		t.Fatal("actual native context did not retain same Agent history", afterTurn, calls.Load())
	}
	after, err := os.ReadFile(canonical)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("readonly native context changed canonical", err)
	}
}
