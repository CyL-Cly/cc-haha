package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/curepl"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestComputerUseActualGoWorkerChild(t *testing.T) {
	for _, argument := range os.Args {
		if argument == "-test.run=^TestComputerUseActualGoWorkerChild$" {
			if e := curepl.RunWorker(context.Background(), os.Stdin, os.Stdout); e != nil {
				os.Stderr.WriteString(e.Error())
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
}

func computerUseMainFixture(t *testing.T) (nativePluginFixture, *computeruse.Service, *NativeComputerUseOptions) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("actual macOS isolated kernel")
	}
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"future":{"keep":true}}`)
	f.write(t, filepath.Join(f.cwd, ".claude", "settings.json"), `{}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "loopback-cu-fake-key"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
	f.n.Environment["DISABLE_TELEMETRY"] = "1"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	_ = os.MkdirAll(f.n.Temp, 0700)
	helper := filepath.Join(f.home, "cc-haha-computer-use.app", "Contents", "MacOS", "cc-haha-computer-use")
	f.write(t, helper, "private external helper placeholder")
	_ = os.Chmod(helper, 0700)
	f.n.Environment["CC_HAHA_CU_HELPER_PATH"] = helper
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	service, e := computeruse.New(computeruse.Options{HomeDir: f.home, ConfigDir: f.config, WorkDir: f.cwd, TempDir: f.n.Temp, Executable: executable, Environment: f.n.Environment, Platform: "darwin", Run: func(_ context.Context, file string, args []string, _ []*os.File) (computeruse.CommandResult, error) {
		if file != "/usr/bin/uname" || !reflect.DeepEqual(args, []string{"-r"}) {
			return computeruse.CommandResult{}, fmt.Errorf("unexpected external launch %s %v", file, args)
		}
		return computeruse.CommandResult{Stdout: "25.4.0"}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	f.write(t, filepath.Join(f.config, "cc-haha", "computer-use-config.json"), `{"enabled":false,"future":{"keep":true}}`)
	if e := service.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true,"authorizedApps":[],"grantFlags":{"systemKeyCombos":false}}`)); e != nil {
		t.Fatal(e)
	}
	options := &NativeComputerUseOptions{Service: service, Executable: executable, RuntimeOptions: &curepl.RuntimeOptions{Executable: executable, TempDir: f.n.Temp, Platform: "darwin", WorkerArguments: []string{"-test.run=^TestComputerUseActualGoWorkerChild$"}}}
	return f, service, options
}

func TestNativeComputerUseSharedMainPrintTwoTurnsAutoPermissionAndNativeTarget(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	var pngBytes bytes.Buffer
	_ = png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 3, 2)))
	screenshot := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	marker := filepath.Join(f.cwd, "native-action.txt")
	var mu sync.Mutex
	calls := []string{}
	frames := []json.RawMessage{}
	requests := 0
	options.NativeCall = func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		mu.Lock()
		calls = append(calls, name)
		mu.Unlock()
		switch name {
		case "check_permissions":
			return json.RawMessage(`{"accessibility":true,"screenRecording":true}`), nil
		case "resolve_app_target":
			return json.RawMessage(`{"pid":42,"bundleId":"private.app","path":"/private/App.app","executablePath":"/private/App.app/Contents/MacOS/App","launchTime":17}`), nil
		case "get_app_state":
			encoded, _ := json.Marshal(map[string]any{"axText": "App=private.app\n  g1:4 Button", "bundleId": "private.app", "screenshot": map[string]string{"base64": screenshot, "mimeType": "image/png"}})
			return encoded, nil
		case "type_text":
			var payload protocol.JSONObject
			_ = payload.UnmarshalJSON(args)
			if string(payload.Get("pid")) != "42" || payload.Get("expectedProcessIdentity") == nil {
				t.Fatalf("unresolved native mutation=%s", args)
			}
			if e := os.WriteFile(marker, []byte(payload.String("text")), 0600); e != nil {
				return nil, e
			}
			return json.RawMessage(`{}`), nil
		default:
			return nil, fmt.Errorf("unexpected native action %s", name)
		}
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests++
		round := requests
		mu.Unlock()
		if !bytes.Contains(body, []byte(`mcp__computer-use__js`)) || bytes.Contains(body, []byte(`mcp__computer-use__click`)) {
			t.Errorf("wrong advertised CU tools=%s", body)
		}
		var code string
		switch round {
		case 1:
			code = `var app = await cua.getApp("Private"); await app.getScreenshot(); nodeRepl.write("first bound")`
		case 3:
			code = `await app.typeText("second-turn-native-write"); nodeRepl.write("binding retained")`
		case 2, 4:
			if !bytes.Contains(body, []byte(`tool_result`)) {
				t.Errorf("real tool result absent=%s", body)
			}
		default:
			t.Errorf("unexpected model request %d", round)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture-message\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		if code != "" {
			input, _ := json.Marshal(map[string]any{"code": code})
			inputString, _ := json.Marshal(string(input))
			fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"cu-%d\",\"name\":\"mcp__computer-use__js\",\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", round, inputString)
		} else {
			fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{Model: "fixture-model", SystemPrompt: "actual shared CU fixture", Tools: []string{"Read,Write"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 8, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func(frame []byte) error {
		mu.Lock()
		frames = append(frames, append(json.RawMessage(nil), frame...))
		mu.Unlock()
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e := m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		status, e := m.mcpTools.ServerStatuses(context.Background(), scope)
		if e != nil {
			return e
		}
		if len(status) != 1 || status[0].String("name") != "computer-use" || status[0].String("status") != "connected" || status[0].String("scope") != "dynamic" {
			t.Fatalf("internal SDK status=%v", status)
		}
		info, _ := status[0].Object("serverInfo")
		if info.String("version") != "0.1.3" {
			t.Fatal("Source SDK server metadata missing")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		var input protocol.JSONObject
		_ = input.UnmarshalJSON([]byte(fmt.Sprintf(`{"type":"user","uuid":"turn-%d","message":{"role":"user","content":"operate private native fixture"}}`, i)))
		if e := m.RunTurn(context.Background(), input); e != nil {
			t.Fatal(e)
		}
		if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			return m.mcpTools.Reconnect(context.Background(), scope, "computer-use")
		}); e != nil {
			t.Fatal(e)
		}
		if _, e := os.Stat(filepath.Join(f.config, "computer-use.lock")); !os.IsNotExist(e) {
			t.Fatalf("turn %d retained CU lease: %v", i, e)
		}
	}
	data, e := os.ReadFile(marker)
	if e != nil || string(data) != "second-turn-native-write" {
		t.Fatalf("actual OS boundary write=%s %v", data, e)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 4 {
		t.Fatalf("physical model calls=%d", requests)
	}
	if len(calls) == 0 {
		t.Fatal("shared Harness bypassed native facade")
	}
	joined, _ := json.Marshal(frames)
	if !bytes.Contains(joined, []byte("binding retained")) || !bytes.Contains(joined, []byte("Image 1: 3×2 pixels")) {
		t.Fatalf("missing original shared toolresult/caption=%s", joined)
	}
}

func TestNativeComputerUseAbortDrainsNativeMutationAndReleasesLease(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	entered, drained := make(chan struct{}), make(chan struct{})
	options.NativeCall = func(ctx context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		switch name {
		case "check_permissions":
			return json.RawMessage(`{"accessibility":true,"screenRecording":true}`), nil
		case "resolve_app_target":
			return json.RawMessage(`{"pid":42,"bundleId":"private.app","path":"/private/App.app","executablePath":"/private/App.app/Contents/MacOS/App","launchTime":17}`), nil
		case "get_app_state":
			return json.RawMessage(`{"axText":"App=private.app\n g1:4 Button","bundleId":"private.app"}`), nil
		case "type_text":
			close(entered)
			<-ctx.Done()
			close(drained)
			return nil, ctx.Err()
		default:
			return nil, fmt.Errorf("unexpected native call %s", name)
		}
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		args, _ := json.Marshal(map[string]any{"code": `var app=await cua.getApp("Private");await app.typeText("cancelled")`})
		part, _ := json.Marshal(string(args))
		fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"cancel-cu\",\"name\":\"mcp__computer-use__js\",\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", part)
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{Model: "fixture-model", SystemPrompt: "actual CU cancellation fixture", Tools: []string{"Read"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 4, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func([]byte) error { return nil }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e := m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var input protocol.JSONObject
	_ = input.UnmarshalJSON([]byte(`{"type":"user","message":{"role":"user","content":"cancel native operation"}}`))
	go func() { done <- m.RunTurn(ctx, input) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("native mutation did not start")
	}
	lease, e := os.ReadFile(filepath.Join(f.config, "computer-use.lock"))
	if e != nil || !bytes.Contains(lease, []byte(m.Controller.SessionID())) {
		t.Fatalf("active operation lacks actual SID lease %s %v", lease, e)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled shared turn did not finish")
	}
	select {
	case <-drained:
	default:
		t.Fatal("lease released before actual native mutation drained")
	}
	if _, e := os.Stat(filepath.Join(f.config, "computer-use.lock")); !os.IsNotExist(e) {
		t.Fatalf("abort retained native lease %v", e)
	}
}
