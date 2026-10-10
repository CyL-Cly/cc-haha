package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"golang.org/x/net/websocket"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDedicatedSDKSocketSharesDesktopPermissionTransportWithoutSecondAgent(t *testing.T) {
	var calls, opens atomic.Int32
	var target string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch calls.Add(1) {
		case 1:
			response(w, "native-sdk-write", "Write", map[string]any{"file_path": target, "content": "NATIVE_SDK_COMPLETE"}, "")
		case 2:
			if bytes.Contains(body, []byte("FOREIGN_SDK_TEXT")) {
				t.Error("foreign SDK projection became native model history")
			}
			if !bytes.Contains(body, []byte("NATIVE_SDK_COMPLETE")) {
				t.Error("native permission/tool result lost")
			}
			response(w, "native-sdk-done", "", nil, "NATIVE SDK FINISHED")
		default:
			t.Error("second Harness or unexpected model call")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.WorkDir, _ = filepath.EvalSymlinks(o.WorkDir)
	o.Native.CWD = o.WorkDir
	o.Arguments.PermissionMode = "default"
	target = filepath.Join(o.WorkDir, "native-sdk-file.txt")
	opened := make(chan server.SessionOptions, 4)
	factory := DesktopFactory(o)
	s, e := server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Factory: func(ctx context.Context, opt server.SessionOptions, emit server.Emit) (server.LiveSession, error) {
		opens.Add(1)
		live, err := factory(ctx, opt, emit)
		opened <- opt
		return live, err
	}})
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, listener) }()
	base := "http://" + listener.Addr().String()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-served:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(15 * time.Second):
			t.Error("server SDK shutdown did not drain")
		}
	})
	request := func(method, path, body string, status int) json.RawMessage {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != status {
			t.Fatalf("%s %s %d %s", method, path, res.StatusCode, data)
		}
		return data
	}
	created := request("POST", "/api/sessions", string(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "default"})), 201)
	id := obj(created).String("sessionId")
	request("POST", "/api/conversations/"+id, `{"content":"REST only queues"}`, 202)
	if calls.Load() != 0 || opens.Load() != 0 {
		t.Fatal("REST 202 started an Agent")
	}
	status := obj(request("GET", "/api/sessions/"+id+"/chat/status", "", 200))
	if status.String("state") != "thinking" || status.String("activityState") != "running" {
		t.Fatal(string(raw(status)))
	}
	dial := func(path string) *websocket.Conn {
		t.Helper()
		socket, err := websocket.Dial("ws"+strings.TrimPrefix(base, "http")+path, "", base)
		if err != nil {
			t.Fatal(err)
		}
		_ = socket.SetDeadline(time.Now().Add(20 * time.Second))
		t.Cleanup(func() { _ = socket.Close() })
		return socket
	}
	send := func(socket *websocket.Conn, value any) {
		t.Helper()
		if err := websocket.Message.Send(socket, string(raw(value))); err != nil {
			t.Fatal(err)
		}
	}
	read := func(socket *websocket.Conn, kind string) json.RawMessage {
		t.Helper()
		for i := 0; i < 100; i++ {
			var data string
			if err := websocket.Message.Receive(socket, &data); err != nil {
				t.Fatal(err)
			}
			m := obj([]byte(data))
			if m.String("type") == "error" {
				t.Fatal(data)
			}
			if m.String("type") == kind {
				return []byte(data)
			}
		}
		t.Fatal("missing", kind)
		return nil
	}
	desktop := dial("/ws/" + id)
	read(desktop, "connected")
	send(desktop, map[string]any{"type": "user_message", "content": "write native file with approval"})
	nativeAsk := read(desktop, "permission_request")
	nativeID := obj(nativeAsk).String("requestId")
	opt := <-opened
	token := opt.Environment["CC_HAHA_SESSION_COLLABORATION_TOKEN"]
	if token == "" {
		t.Fatal("owned SDK credential missing")
	}
	sdk := dial("/sdk/" + id + "?token=" + token)
	send(sdk, map[string]any{"type": "assistant", "uuid": "foreign-replay-1", "message": map[string]any{"id": "foreign-api", "content": []map[string]any{{"type": "text", "text": "FOREIGN_SDK_TEXT"}}}})
	read(desktop, "content_delta")
	send(sdk, map[string]any{"type": "control_request", "request_id": "foreign-question", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "AskUserQuestion", "input": map[string]any{"metadata": map[string]any{"autoAnswered": true}}, "permission_suggestions": []any{map[string]any{"type": "addRules", "destination": "userSettings"}}}})
	foreignAsk := read(desktop, "permission_request")
	if obj(foreignAsk).String("requestId") != "foreign-question" {
		t.Fatal(string(foreignAsk))
	}
	request("POST", "/api/conversations/"+id+"/stop", "", 200)
	status = obj(request("GET", "/api/conversations/"+id+"/status", "", 200))
	if status.String("state") != "idle" || status.String("activityState") != "waiting" {
		t.Fatal("REST stop incorrectly canceled real native turn", string(raw(status)))
	}
	// Replace the SDK peer, then detach its predecessor. The new response route
	// must survive, and both native and foreign permissions survive UI reconnect.
	sdk2 := dial("/sdk/" + id + "?token=" + token)
	_ = sdk.Close()
	_ = desktop.Close()
	desktop = dial("/ws/" + id)
	read(desktop, "connected")
	permissions := map[string]bool{}
	for i := 0; i < 100; i++ {
		var data string
		if e = websocket.Message.Receive(desktop, &data); e != nil {
			t.Fatal(e)
		}
		m := obj([]byte(data))
		if m.String("type") == "permission_request" {
			permissions[m.String("requestId")] = true
		}
		if m.String("type") == "permission_requests_snapshot" {
			if !bytes.Contains(m.Get("toolRequestIds"), []byte("foreign-question")) {
				t.Fatal("foreign pending missing from authoritative snapshot", data)
			}
			break
		}
	}
	if !permissions[nativeID] || !permissions["foreign-question"] {
		t.Fatal("pending permissions lost", permissions)
	}
	send(desktop, map[string]any{"type": "permission_response", "requestId": "foreign-question", "allowed": true, "rule": "always", "updatedInput": map[string]any{"answers": map[string]string{"q": "Go"}, "metadata": map[string]any{"autoAnswered": true, "keep": 2}}})
	sdkResponse := read(sdk2, "control_response")
	if !bytes.Contains(sdkResponse, []byte(`"destination":"session"`)) || bytes.Contains(sdkResponse, []byte("autoAnswered")) {
		t.Fatal("SDK response", string(sdkResponse))
	}
	if _, e = os.Stat(target); !os.IsNotExist(e) || calls.Load() != 1 {
		t.Fatal("foreign approval resolved native future", e, calls.Load())
	}
	send(desktop, map[string]any{"type": "permission_response", "requestId": nativeID, "allowed": true})
	read(desktop, "message_complete")
	data, e := os.ReadFile(target)
	if e != nil || string(data) != "NATIVE_SDK_COMPLETE" || calls.Load() != 2 || opens.Load() != 1 {
		t.Fatal("shared native Agent", e, string(data), calls.Load(), opens.Load())
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	transcript, e := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(o.WorkDir), id+".jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(transcript, []byte("FOREIGN_SDK_TEXT")) {
		t.Fatal("SDK transport recorded a second conversation")
	}
	var closed string
	if e = websocket.Message.Receive(sdk2, &closed); e == nil {
		t.Fatal("SDK socket survived server shutdown", closed)
	}
}
