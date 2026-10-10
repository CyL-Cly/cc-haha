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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"golang.org/x/net/websocket"
)

func TestActualNativeTraceCloneReadsUnconsumedResponseTail(t *testing.T) {
	const tail = "PHYSICAL_TAIL_AFTER_SDK_MESSAGE_STOP_83C9"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		response(w, "actual-trace-stream", "", nil, "ACTUAL_NATIVE_TRACE_OK")
		w.(http.Flusher).Flush()
		// The SDK's own decoder finishes before this physical response ends.
		time.Sleep(30 * time.Millisecond)
		_, _ = io.WriteString(w, tail)
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Native.Environment["CC_HAHA_TRACE_API_CALLS"] = "1"
	trace, err := tracecapture.New(tracecapture.Options{Scope: o.Environment["CLAUDE_CONFIG_DIR"]})
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close(context.Background())
	o.Trace = trace
	session, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = session.Submit(ctx, tui.Input{Text: "Run an actual traced request."}, nil); err != nil {
		t.Fatal(err)
	}
	if err = trace.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	full, err := trace.Full(ctx, o.Arguments.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var calls []protocol.JSONObject
	if err = json.Unmarshal(full.Get("calls"), &calls); err != nil || len(calls) != 1 {
		t.Fatalf("actual trace calls %s %v", raw(full), err)
	}
	call := calls[0]
	preview := obj(obj(call.Get("response")).Get("body")).String("preview")
	if call.String("status") != "ok" || !strings.HasSuffix(preview, tail) || !strings.Contains(preview, "ACTUAL_NATIVE_TRACE_OK") {
		t.Fatalf("independent physical clone lost SDK or tail bytes: %s", raw(call))
	}
	requestBody := obj(obj(call.Get("request")).Get("body"))
	if bytes.Contains(raw(call), []byte("fake-only-loopback")) || !strings.Contains(requestBody.String("preview"), `"model": "fixture-model"`) {
		t.Fatal("actual request capture leaked authentication or changed model/body snapshot")
	}
}

func TestDesktopTraceFactoryCapturesActualWebSocketTurnAndSettings(t *testing.T) {
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		calls.Add(1)
		response(w, "desktop-trace", "", nil, "DESKTOP_TRACE_FACTORY_OK")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Environment["CC_HAHA_TRACE_PROVIDER_NAME"] = "wrong-inherited-provider"
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, Arguments: o.Arguments, Version: o.Version, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	request := func(method, path string, value any) protocol.JSONObject {
		t.Helper()
		r, _ := http.NewRequest(method, host.URL+path, bytes.NewReader(raw(value)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer fake-only-loopback")
		res, e := host.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 && res.StatusCode != 201 {
			t.Fatalf("actual %s %s %d %s", method, path, res.StatusCode, data)
		}
		return obj(data)
	}
	if string(request("GET", "/api/traces/settings", nil).Get("enabled")) != "true" {
		t.Fatal("Trace settings not wired")
	}
	created := request("POST", "/api/sessions", map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})
	id := created.String("sessionId")
	wsConfig, err := websocket.NewConfig("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, host.URL)
	if err != nil {
		t.Fatal(err)
	}
	wsConfig.Header.Set("Authorization", "Bearer fake-only-loopback")
	ws, err := websocket.DialConfig(wsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(10 * time.Second))
	if err = websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "Capture the actual native turn."}))); err != nil {
		t.Fatal(err)
	}
	finished := false
	for i := 0; i < 100; i++ {
		var frame string
		if err = websocket.Message.Receive(ws, &frame); err != nil {
			t.Fatal(err)
		}
		message := obj([]byte(frame))
		if message.String("type") == "error" {
			t.Fatal(frame)
		}
		finished = finished || message.String("type") == "message_complete"
		if finished && message.String("type") == "session_state" && message.String("turnState") == "idle" {
			break
		}
	}
	if !finished || calls.Load() != 1 {
		t.Fatal("actual shared turn did not complete", finished, calls.Load())
	}
	// Server.Close drains its real shared Trace writer and all session leases.
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(config, "cc-haha", "traces", id+".jsonl"))
	if err != nil || !bytes.Contains(data, []byte("DESKTOP_TRACE_FACTORY_OK")) || bytes.Contains(data, []byte("wrong-inherited-provider")) || bytes.Contains(data, []byte("fake-only-loopback")) {
		t.Fatalf("factory capture/drain/provider provenance %s %v", data, err)
	}
}
