package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
)

func TestDesktopRuntimeSwitchWaitsForActiveTurnThenRestoresHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var oldCalls, newCalls atomic.Int32
	oldProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if oldCalls.Add(1) != 1 {
			t.Error("old provider reused after runtime switch")
			http.Error(w, "unexpected request", 400)
			return
		}
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		response(w, "old-runtime", "", nil, "OLD PROVIDER ANSWER")
	}))
	defer oldProvider.Close()
	defer releaseOnce.Do(func() { close(release) })
	newProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newCalls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("OLD PROVIDER ANSWER")) {
			t.Error("runtime switch lost original assistant history")
		}
		if r.Header.Get("X-Api-Key") != "second-provider-key" {
			t.Errorf("runtime switch kept old credentials: %q", r.Header.Get("X-Api-Key"))
		}
		response(w, "new-runtime", "", nil, "NEW PROVIDER ANSWER")
	}))
	defer newProvider.Close()
	o := nativeSessionOptions(t, oldProvider.URL)
	managed := o.Native.ManagedRoot
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, fmt.Errorf("isolated runtime Keychain unavailable")
	}
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		native, e := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		if e == nil {
			native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
				return nil, fmt.Errorf("isolated runtime Keychain unavailable")
			}
		}
		return native, e
	}
	index := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", "providers.json")
	if err := os.MkdirAll(filepath.Dir(index), 0700); err != nil {
		t.Fatal(err)
	}
	// The initial process loads the active provider during real post-trust
	// startup. Keep that route on the old endpoint until the explicit second
	// selection; a preselected second default never exercises runtime handoff.
	if err := os.WriteFile(index, raw(map[string]any{"schemaVersion": 6, "activeId": "first", "providers": []any{map[string]any{"id": "first", "presetId": "deepseek", "name": "initial loopback runtime", "baseUrl": oldProvider.URL, "apiKey": "fake-only-loopback", "apiFormat": "anthropic", "authStrategy": "api_key", "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}}, map[string]any{"id": "second", "presetId": "deepseek", "name": "loopback runtime", "baseUrl": newProvider.URL, "apiKey": "second-provider-key", "apiFormat": "anthropic", "authStrategy": "api_key", "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}}}}), 0600); err != nil {
		t.Fatal(err)
	}
	// Desktop sessions are created by the server before the runtime starts.
	// Exercise that real boundary: Source creates its initial snapshot and
	// session metadata before an independent CLI persistence job can run.
	host, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: o.NativeFactory})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	created := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
	request.RemoteAddr = "127.0.0.1:32100"
	request.Host = "127.0.0.1:3000"
	request.Header.Set("Content-Type", "application/json")
	host.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("actual desktop session creation: %d %s", created.Code, created.Body.String())
	}
	o.Arguments.SessionID = obj(created.Body.Bytes()).String("sessionId")
	if o.Arguments.SessionID == "" {
		t.Fatal("actual desktop session creation omitted its ID")
	}
	frames := make(chan json.RawMessage, 256)
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir}, func(frame json.RawMessage) { frames <- append(json.RawMessage(nil), frame...) })
	if err != nil {
		t.Fatal(err)
	}
	d := live.(*DesktopSession)
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	done := make(chan error, 1)
	go func() { done <- d.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "first turn"})) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	before := d.currentEngine()
	if err = d.Handle(ctx, raw(map[string]any{"type": "set_runtime_config", "providerId": "second", "modelId": "fixture-model"})); err != nil {
		t.Fatal(err)
	}
	if newCalls.Load() != 0 || d.currentEngine() != before {
		t.Fatal("runtime restarted before the active ASK finished")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ack := false
	for len(frames) > 0 {
		frame := obj(<-frames)
		if frame.String("type") == "runtime_config_applied" {
			ack = frame.String("providerId") == "second"
		}
	}
	if !ack {
		t.Fatal("missing actual runtime acknowledgement")
	}
	if d.currentEngine() == before {
		t.Fatal("confirmed durable runtime did not replace its engine")
	}
	if e := before.process.Owner.Run(ctx, func(*cooperative.Scope) error { return nil }); e != cooperative.ErrClosed {
		t.Fatalf("old real Owner survived confirmed replacement: %v", e)
	}

	if err = d.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "continue after switch"})); err != nil {
		t.Fatal(err)
	}
	if oldCalls.Load() != 1 || newCalls.Load() != 1 {
		t.Fatalf("old=%d new=%d", oldCalls.Load(), newCalls.Load())
	}
	if err = d.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if e := d.currentEngine().process.Owner.Run(ctx, func(*cooperative.Scope) error { return nil }); e != cooperative.ErrClosed {
		t.Fatalf("new real Owner survived desktop close: %v", e)
	}

	var transcript []byte
	_ = filepath.WalkDir(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && filepath.Ext(path) == ".jsonl" {
			data, _ := os.ReadFile(path)
			transcript = append(transcript, data...)
		}
		return err
	})
	if !bytes.Contains(transcript, []byte("OLD PROVIDER ANSWER")) || !bytes.Contains(transcript, []byte("NEW PROVIDER ANSWER")) {
		t.Fatal("actual old/new replies not retained in canonical transcript")
	}
	if !bytes.Contains(transcript, []byte(`"type":"session-meta"`)) || !bytes.Contains(transcript, []byte(`"runtimeProviderId":"second"`)) {
		t.Fatalf("runtime metadata was not persisted through the shared Writer: %s", transcript)
	}
}

func TestFirstRuntimeSelectionStartsFreshAndFailedRestartCanRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("first turn")) {
			response(w, "first", "", nil, "FIRST TURN OK")
		} else {
			response(w, "second", "", nil, "SECOND TURN OK")
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	managed := o.Native.ManagedRoot
	var reject atomic.Bool
	reject.Store(true)
	o.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		if reject.Load() {
			return nil, errors.New("deliberate isolated factory failure")
		}
		native, e := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		if e == nil {
			native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
				return nil, fmt.Errorf("isolated runtime Keychain unavailable")
			}
		}
		return native, e
	}
	index := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", "providers.json")
	if err := os.MkdirAll(filepath.Dir(index), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, raw(map[string]any{"schemaVersion": 6, "activeId": "second", "providers": []any{map[string]any{"id": "second", "presetId": "deepseek", "name": "loopback runtime", "baseUrl": provider.URL, "apiKey": "second-provider-key", "apiFormat": "anthropic", "authStrategy": "api_key", "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}}}}), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	frames := []json.RawMessage{}
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir}, func(v json.RawMessage) {
		mu.Lock()
		frames = append(frames, append(json.RawMessage(nil), v...))
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	d := live.(*DesktopSession)
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	config := raw(map[string]any{"type": "set_runtime_config", "providerId": "second", "modelId": "fixture-model"})
	if err = d.Handle(ctx, config); err == nil || !strings.Contains(err.Error(), "deliberate isolated factory failure") {
		t.Fatalf("failure not propagated: %v", err)
	}
	if d.currentEngine().closed {
		t.Fatal("failed restart left the closed Owner as runtime")
	}
	if err = d.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "must remain blocked"})); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("rejected runtime selection sent a model request")
	}
	reject.Store(false)
	if err = d.Handle(ctx, config); err != nil {
		t.Fatalf("first selection attempted to resume metadata-only session: %v", err)
	}
	if err = d.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "first turn"})); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	ack := false
	for _, v := range frames {
		if obj(v).String("type") == "runtime_config_applied" {
			ack = true
		}
	}
	if !ack {
		t.Fatal("valid retry did not acknowledge runtime selection")
	}
}
