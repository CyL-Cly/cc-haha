package application

import (
	"bufio"
	"bytes"
	"context"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/net/websocket"
)

func TestActualDesktopSideQuestionAPIAbortAndCloseOwnOnlyFork(t *testing.T) {
	entered, canceled := make(chan struct{}, 2), make(chan struct{}, 2)
	var calls atomic.Int32
	var forbiddenPath string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		calls.Add(1)
		if bytes.Contains(data, []byte("This is a side question from the user")) {
			if bytes.Contains(data, []byte("held side")) {
				entered <- struct{}{}
				<-r.Context().Done()
				canceled <- struct{}{}
				return
			}
			if bytes.Contains(data, []byte("forbidden side")) {
				response(w, "forbidden", "Write", map[string]any{"file_path": forbiddenPath, "content": "forbidden"}, "")
				return
			}
			response(w, "side", "", nil, "SIDESAFE")
			return
		}
		response(w, "main", "", nil, "PARENT ONLY")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.Tools = nil
	delete(o.Environment, "CLAUDE_CODE_SIMPLE")
	forbiddenPath = filepath.Join(o.WorkDir, "side-must-not-write.txt")
	s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		n, e := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		if e == nil {
			n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }
		}
		return n, e
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	status499 := make(chan struct{}, 2)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &sideStatusWriter{ResponseWriter: w, on499: func() { status499 <- struct{}{} }}
		s.ServeHTTP(writer, r)
	}))
	defer host.Close()
	request := func(method, path string, body any, status int) protocol.JSONObject {
		t.Helper()
		r, _ := http.NewRequest(method, host.URL+path, bytes.NewReader(raw(body)))
		r.Header.Set("Content-Type", "application/json")
		res, e := host.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != status {
			t.Fatalf("HTTP %s %s status%d body%s", method, path, res.StatusCode, data)
		}
		return obj(data)
	}
	id := request("POST", "/api/sessions", map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"}, 201).String("sessionId")
	ws, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if e != nil {
		t.Fatal(e)
	}
	defer ws.Close()
	receive := func(kind string) protocol.JSONObject {
		t.Helper()
		_ = ws.SetReadDeadline(time.Now().Add(12 * time.Second))
		for {
			var data string
			if e := websocket.Message.Receive(ws, &data); e != nil {
				t.Fatal("WS", kind, e)
			}
			row := obj([]byte(data))
			if row.String("type") == "error" {
				t.Fatal(data)
			}
			if row.String("type") == kind {
				return row
			}
		}
	}
	receive("connected")
	turn := func(text string) {
		t.Helper()
		if e := websocket.JSON.Send(ws, map[string]any{"type": "user_message", "content": text}); e != nil {
			t.Fatal(e)
		}
		receive("message_complete")
		for state := receive("session_state"); state.String("turnState") != "idle"; state = receive("session_state") {
		}
	}
	turn("seed parent")
	path := "/api/sessions/" + id + "/side-question"
	answer := request("POST", path, map[string]any{"questionId": "11111111-1111-4111-8111-111111111111", "question": "safe side", "history": []any{}}, 200)
	if answer.String("response") != "SIDESAFE" {
		t.Fatal(answer)
	}
	bad := request("POST", path, map[string]any{"questionId": "22222222-2222-4222-8222-222222222222", "question": "forbidden side"}, 502)
	if bad.String("message") != "Side questions cannot call Write" {
		t.Fatal("early tool rejection", bad)
	}
	if _, e := os.Stat(forbiddenPath); !os.IsNotExist(e) {
		t.Fatal("side tool ran", e)
	}
	startHeld := func(qid string) (context.CancelFunc, <-chan error) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		r, _ := http.NewRequestWithContext(ctx, "POST", host.URL+path, bytes.NewReader(raw(map[string]any{"questionId": qid, "question": "held side"})))
		r.Header.Set("Content-Type", "application/json")
		go func() {
			res, e := host.Client().Do(r)
			if e == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				res.Body.Close()
			}
			done <- e
		}()
		select {
		case <-entered:
		case e := <-done:
			t.Fatal("held side startup", e)
		case <-time.After(12 * time.Second):
			t.Fatal("held side never reached actual model")
		}
		return cancel, done
	}
	cancel, done := startHeld("33333333-3333-4333-8333-333333333333")
	// Main completes while side HTTP remains pending. Closing the HTTP client
	// cancels exactly this side query through original req.signal semantics.
	turn("main while side held")
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("client abort failed")
	}
	select {
	case <-status499:
	case <-time.After(3 * time.Second):
		t.Fatal("response race did not preserve499")
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("side socket escaped HTTP abort")
	}
	turn("parent after side cancellation")
	cancel2, done2 := startHeld("44444444-4444-4444-8444-444444444444")
	defer cancel2()
	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close(context.Background()) }()
	select {
	case e := <-closeDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Server.Close did not join side and main owners")
	}
	select {
	case <-done2:
	case <-time.After(3 * time.Second):
		t.Fatal("side API escaped administrative Close")
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("Close released native resources before side socket cancellation")
	}
	if calls.Load() != 7 {
		t.Fatal("physical model calls", calls.Load())
	}
	files, _ := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", id+".jsonl"))
	if len(files) != 1 {
		t.Fatal("parent canonical path", files)
	}
	transcript, e := os.ReadFile(files[0])
	if e != nil {
		t.Fatal(e)
	}
	for _, marker := range []string{"SIDESAFE", "This is a side question from the user", "held side", "forbidden side"} {
		if bytes.Contains(transcript, []byte(marker)) {
			t.Fatal("ephemeral side leaked to parent canonical", marker)
		}
	}
}

type sideStatusWriter struct {
	http.ResponseWriter
	on499 func()
}

func (w *sideStatusWriter) WriteHeader(status int) {
	if status == 499 {
		w.on499()
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *sideStatusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *sideStatusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}
