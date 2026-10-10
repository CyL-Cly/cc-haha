package imruntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/application"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func feishuRaw(v any) json.RawMessage { p, _ := json.Marshal(v); return p }
func feishuObj(p []byte) protocol.JSONObject {
	o := protocol.NewJSONObject()
	_ = o.UnmarshalJSON(p)
	return o
}

type feishuLoopbackTransport struct {
	peer      *url.URL
	transport http.RoundTripper
}

func (t feishuLoopbackTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	u := *c.URL
	u.Scheme = t.peer.Scheme
	u.Host = t.peer.Host
	c.URL = &u
	return t.transport.RoundTrip(c)
}
func feishuModelResponse(w http.ResponseWriter, id, tool string, input any, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(kind string, value any) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, feishuRaw(value)) }
	emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 12, "output_tokens": 1, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}}})
	stop := "end_turn"
	if tool != "" {
		stop = "tool_use"
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "call-" + id, "name": tool, "input": map[string]any{}}})
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(feishuRaw(input))}})
	} else {
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}})
	}
	emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 8}})
	emit("message_stop", map[string]any{"type": "message_stop"})
}

func TestActualFeishuSharedMainPrintWriteReadPermissionCardAndSameSessionResume(t *testing.T) {
	root := t.TempDir()
	home, work, managed := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "managed")
	for _, dir := range []string{home, work, managed} {
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	configDir := filepath.Join(home, ".claude")
	os.MkdirAll(configDir, 0700)
	target := filepath.Join(work, "feishu-mainprint-file.txt")
	var requests atomic.Int32
	var requestMu sync.Mutex
	modelBodies := []json.RawMessage{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := io.ReadAll(r.Body)
		requestMu.Lock()
		modelBodies = append(modelBodies, p)
		requestMu.Unlock()
		switch requests.Add(1) {
		case 1:
			feishuModelResponse(w, "write", "Write", map[string]string{"file_path": target, "content": "FEISHU_SHARED_MAINPRINT_FILE\n"}, "")
		case 2:
			feishuModelResponse(w, "read", "Read", map[string]string{"file_path": target}, "")
		case 3:
			feishuModelResponse(w, "finish", "", nil, "FEISHU_SHARED_MAINPRINT_DONE")
		case 4:
			feishuModelResponse(w, "resume-read", "Read", map[string]string{"file_path": target}, "")
		case 5:
			feishuModelResponse(w, "resume-finish", "", nil, "FEISHU_SHARED_MAINPRINT_RESUMED")
		default:
			t.Error("extra/duplicate actual shared Harness request")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": configDir, "ANTHROPIC_BASE_URL": model.URL, "ANTHROPIC_API_KEY": "fake-only-loopback", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "DISABLE_TELEMETRY": "1", "PATH": "/usr/bin:/bin", "TZ": "UTC", "CC_HAHA_LOCAL_ACCESS_TOKEN": "private-feishu-server"}
	native, e := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: work, Platform: "darwin", ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	args := clibootstrap.PrintArguments{Model: "fixture-model", SystemPrompt: "Use the actual tools to execute the requested test.", Tools: []string{"Read,Write,Edit,Bash,Glob,Grep"}, MaxTurns: 20, PermissionMode: "default", Sources: clibootstrap.DefaultState()}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	server, e := application.NewDesktopServer(application.ServerOptions{Port: listener.Addr().(*net.TCPAddr).Port, Environment: env, HomeDir: home, ConfigDir: configDir, WorkDir: work, Version: "999.0.0-feishu-test", Arguments: args, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(environ map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: environ, CWD: cwd, Platform: platform, ManagedRoot: native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close(context.Background())
	desktop := httptest.NewUnstartedServer(server)
	desktop.Listener.Close()
	desktop.Listener = listener
	desktop.Start()
	defer desktop.Close()
	approvals := make(chan string, 8)
	finals := make(chan string, 4)
	var approvalCount atomic.Int32
	var cardCount atomic.Int32
	var platformMu sync.Mutex
	packets := []json.RawMessage{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := io.ReadAll(r.Body)
		platformMu.Lock()
		packets = append(packets, p)
		platformMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			w.Write([]byte(`{"tenant_access_token":"private-feishu-token","expire":3600}`))
		case r.URL.Path == "/open-apis/cardkit/v1/cards":
			w.Write(feishuRaw(map[string]any{"code": 0, "data": map[string]string{"card_id": fmt.Sprintf("card-%d", cardCount.Add(1))}}))
		case r.URL.Path == "/open-apis/im/v1/messages":
			body := feishuObj(p)
			content := feishuObj([]byte(body.String("content")))
			if content.String("schema") == "2.0" {
				var extract func(any)
				var decoded any
				_ = json.Unmarshal([]byte(body.String("content")), &decoded)
				extract = func(v any) {
					switch x := v.(type) {
					case map[string]any:
						if x["action"] == "permit" && x["allowed"] == true {
							if id, ok := x["requestId"].(string); ok {
								approvals <- id
								return
							}
						}
						for _, child := range x {
							extract(child)
						}
					case []any:
						for _, child := range x {
							extract(child)
						}
					}
				}
				extract(decoded)
			}
			w.Write([]byte(`{"code":0,"data":{"message_id":"private-message"}}`))
		case strings.HasPrefix(r.URL.Path, "/open-apis/cardkit/v1/cards/"):
			w.Write([]byte(`{"code":0}`))
			if r.Method == "PUT" && !strings.Contains(r.URL.Path, "/elements/") {
				if bytes.Contains(p, []byte("FEISHU_SHARED_MAINPRINT_RESUMED")) {
					finals <- "resumed"
				} else if bytes.Contains(p, []byte("FEISHU_SHARED_MAINPRINT_DONE")) {
					finals <- "done"
				}
			}
		default:
			t.Errorf("unowned platform endpoint %s", r.URL.Path)
			http.Error(w, "unowned", 404)
		}
	}))
	defer peer.Close()
	platformURL, _ := url.Parse(peer.URL)
	client, e := imruntime.NewFeishuClient(context.Background(), imruntime.FeishuClientOptions{Credentials: feishuObj([]byte(`{"appId":"fixture-feishu","appSecret":"fixture-secret"}`)), HTTP: &http.Client{Transport: feishuLoopbackTransport{platformURL, peer.Client().Transport}}, Cache: imruntime.NewFeishuTokenCache()})
	if e != nil {
		t.Fatal(e)
	}
	config := map[string]any{"defaultProjectDir": work, "allowedProjectRoots": []string{work}, "feishu": map[string]any{"appId": "fixture-feishu", "appSecret": "fixture-secret", "allowedUsers": []string{"U1"}}}
	os.WriteFile(filepath.Join(configDir, "adapters.json"), feishuRaw(config), 0600)
	adapterEnv := map[string]string{}
	for k, v := range env {
		adapterEnv[k] = v
	}
	adapterEnv["ADAPTER_SERVER_URL"] = "ws" + strings.TrimPrefix(desktop.URL, "http")
	cfg := imruntime.LoadPlatformConfig("feishu", home, work, adapterEnv, func(string) {})
	runtime, e := imruntime.NewFeishuRuntime(context.Background(), imruntime.FeishuRuntimeOptions{Config: cfg, Client: client, ServerHTTP: desktop.Client()})
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	var current atomic.Pointer[imruntime.FeishuRuntime]
	current.Store(runtime)
	approvalCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	approvalDone := make(chan struct{})
	go func() {
		defer close(approvalDone)
		for {
			select {
			case id := <-approvals:
				approvalCount.Add(1)
				_, e := current.Load().HandleCardAction(feishuObj(feishuRaw(map[string]any{"context": map[string]string{"open_chat_id": "chat-1"}, "action": map[string]any{"value": map[string]any{"action": "permit", "allowed": true, "requestId": id}}})))
				if e != nil {
					t.Error(e)
				}
			case <-approvalCtx.Done():
				return
			}
		}
	}()
	message := func(id, text string) protocol.JSONObject {
		return feishuObj(feishuRaw(map[string]any{"sender": map[string]any{"sender_id": map[string]string{"open_id": "U1"}}, "message": map[string]any{"message_id": id, "chat_id": "chat-1", "chat_type": "p2p", "message_type": "text", "content": string(feishuRaw(map[string]string{"text": text}))}}))
	}
	incoming := message("first", "Write the requested private artifact, read it, then finish.")
	if e = runtime.HandleMessage(incoming); e != nil {
		t.Fatal(e)
	}
	runtime.HandleMessage(incoming)
	wait := func(want string) {
		select {
		case got := <-finals:
			if got != want {
				t.Fatalf("actual final card %s want %s", got, want)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("actual MainPrint/card completion missing", requests.Load(), approvalCount.Load())
		}
	}
	wait("done")
	data, e := os.ReadFile(target)
	if e != nil || string(data) != "FEISHU_SHARED_MAINPRINT_FILE\n" || requests.Load() != 3 || approvalCount.Load() < 1 {
		t.Fatal("actual file/tools/permission/dedup failed", e, requests.Load(), approvalCount.Load())
	}
	stored := runtime.Store.Get("chat-1")
	if stored == nil {
		t.Fatal("actual session not persisted")
	}
	sid := stored.SessionID
	runtime.Close()
	client, e = imruntime.NewFeishuClient(context.Background(), imruntime.FeishuClientOptions{Credentials: feishuObj([]byte(`{"appId":"fixture-feishu","appSecret":"fixture-secret"}`)), HTTP: &http.Client{Transport: feishuLoopbackTransport{platformURL, peer.Client().Transport}}, Cache: imruntime.NewFeishuTokenCache()})
	if e != nil {
		t.Fatal(e)
	}
	runtime, e = imruntime.NewFeishuRuntime(context.Background(), imruntime.FeishuRuntimeOptions{Config: cfg, Client: client, ServerHTTP: desktop.Client()})
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	current.Store(runtime)
	if e = runtime.HandleMessage(message("second", "Read the same file and finish the resumed turn.")); e != nil {
		t.Fatal(e)
	}
	wait("resumed")
	if runtime.Store.Get("chat-1").SessionID != sid || requests.Load() != 5 {
		t.Fatal("actual same-session resume changed identity or model count", requests.Load())
	}
	requestMu.Lock()
	last := modelBodies[len(modelBodies)-1]
	requestMu.Unlock()
	if !bytes.Contains(last, []byte("FEISHU_SHARED_MAINPRINT_FILE")) {
		t.Fatal("actual resumed model history omitted physical Read result")
	}
	runtime.Close()
	before := requests.Load()
	runtime.HandleMessage(message("after-close", "must not execute"))
	if requests.Load() != before {
		t.Fatal("new model task after Close")
	}
	cancel()
	<-approvalDone
}
