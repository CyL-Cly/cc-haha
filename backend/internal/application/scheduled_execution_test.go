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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/scheduler"
)

func TestScheduledExecutorUsesActualSDKToolsAndOriginalTaskEnvironment(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if obj(body).String("model") != "fixture-model" {
			t.Errorf("actual task model changed%s", body)
		}
		switch calls.Add(1) {
		case 1:
			response(w, "scheduled-write", "Write", map[string]any{"file_path": "scheduled-result.txt", "content": "ACTUAL_SCHEDULED_5C4A"}, "")
		case 2:
			response(w, "scheduled-read", "Read", map[string]any{"file_path": "scheduled-result.txt"}, "")
		default:
			response(w, "scheduled-done", "", nil, "SCHEDULED_ACTUAL_SDK_OK")
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = []string{"Write,Read"}
	o.Arguments.AllowedTools = nil
	// This provider must never replace the cron-resolved environment. If the
	// normal desktop resolver runs a second time, this missing ID rejects boot.
	providerDir := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "cc-haha")
	if e := os.MkdirAll(providerDir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(providerDir, "providers.json"), []byte(`{"activeId":"must-not-resolve-again","providers":[]}`), 0600); e != nil {
		t.Fatal(e)
	}
	var passedEnvironment map[string]string
	o.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		passedEnvironment = env
		// This fixture admits one task and owns this boundary exclusively.
		o.Native.Environment = env
		o.Native.CWD = cwd
		return o.Native, nil
	}
	env := make(map[string]string, len(o.Environment))
	for k, v := range o.Environment {
		env[k] = v
	}
	env["CALLER_DIR"] = o.WorkDir
	env["CLAUDE_CODE_ENABLE_TASKS"] = "1"
	request := scheduler.ExecutionRequest{Task: obj([]byte(`{"model":"\ufeff fixture-model \u00a0"}`)), WorkDir: o.WorkDir, SessionID: o.Arguments.SessionID, Environment: env, Input: obj([]byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Execute scheduled Write and Read"}]},"parent_tool_use_id":null,"session_id":""}`))}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var sdk []protocol.JSONObject
	result, e := scheduledExecutor(o)(ctx, request, func(v json.RawMessage) { sdk = append(sdk, obj(v)) })
	if e != nil || result.ExitCode != 0 {
		t.Fatalf("actual scheduled executor%+v %v", result, e)
	}
	if calls.Load() != 3 || passedEnvironment["CALLER_DIR"] != o.WorkDir || passedEnvironment["ANTHROPIC_BASE_URL"] != provider.URL {
		t.Fatal("task environment/tools altered", calls.Load())
	}
	if len(sdk) == 0 || sdk[len(sdk)-1].String("result") != "SCHEDULED_ACTUAL_SDK_OK" {
		t.Fatalf("original SDK output lost%s", raw(sdk))
	}
	data, e := os.ReadFile(filepath.Join(o.WorkDir, "scheduled-result.txt"))
	if e != nil || string(data) != "ACTUAL_SCHEDULED_5C4A" {
		t.Fatalf("real scheduled artifact%q %v", data, e)
	}
}

func TestScheduledExecutorStartupFailureRemainsAnActualChildExit(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.NativeFactory = func(map[string]string, string, string) (*clibootstrap.NativeBoundary, error) {
		return nil, errors.New("ACTUAL_NATIVE_STARTUP_FAILURE_77D1")
	}
	result, err := scheduledExecutor(o)(context.Background(), scheduler.ExecutionRequest{Task: obj([]byte(`{"model":"fixture-model"}`)), WorkDir: o.WorkDir, Environment: o.Environment}, func(json.RawMessage) { t.Error("failed startup emitted SDK output") })
	if err != nil || result.ExitCode != 1 || !strings.Contains(result.Stderr, "ACTUAL_NATIVE_STARTUP_FAILURE_77D1") {
		t.Fatalf("startup failed child classification %+v %v", result, err)
	}
}

func TestScheduledExecutorActualCanceledModelJoinsBeforeReturning(t *testing.T) {
	started, joined := make(chan struct{}), make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		close(started)
		<-r.Context().Done()
		close(joined)
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		o.Native.Environment, o.Native.CWD = env, cwd
		return o.Native, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := scheduler.ExecutionRequest{Task: obj([]byte(`{"model":"fixture-model"}`)), WorkDir: o.WorkDir, SessionID: o.Arguments.SessionID, Environment: o.Environment, Input: obj([]byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Wait for cancellation"}]},"session_id":""}`))}
	type outcome struct {
		result scheduler.ExecutionResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := scheduledExecutor(o)(ctx, request, func(json.RawMessage) {})
		done <- outcome{result, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("actual model request did not start")
	}
	cancel()
	select {
	case result := <-done:
		if result.err != nil || result.result.ExitCode != 143 {
			t.Fatalf("actual canceled child classification %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled shared SDK/native body did not join")
	}
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("scheduled executor returned before physical model request closed")
	}
}
