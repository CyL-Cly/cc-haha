package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func webRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func webWire(model string, blocks []protocol.JSONObject) string {
	var out strings.Builder
	event := func(kind string, v any) { fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", kind, webRaw(v)) }
	event("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "native-web-fixture", "type": "message", "role": "assistant", "model": model, "content": []any{}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 0}}})
	for i, b := range blocks {
		typ := b.String("type")
		copy := b.Clone()
		var delta any
		if typ == "text" {
			copy.Set("text", quoteMain(""))
			delta = map[string]any{"type": "text_delta", "text": b.String("text")}
		} else if typ == "tool_use" || typ == "server_tool_use" {
			copy.Set("input", []byte(`{}`))
			delta = map[string]any{"type": "input_json_delta", "partial_json": string(b.Get("input"))}
		}
		event("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": copy})
		if delta != nil {
			event("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": delta})
		}
		event("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	stopReason := "end_turn"
	for _, block := range blocks {
		if block.String("type") == "tool_use" {
			stopReason = "tool_use"
		}
	}
	event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason}, "usage": map[string]int{"output_tokens": 4}})
	event("message_stop", map[string]any{"type": "message_stop"})
	return out.String()
}
func webObject(data string) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON([]byte(data))
	return o
}
func TestSharedMainWebFetchUsesActualHTTPSecondaryAccountingAndCache(t *testing.T) {
	var mu sync.Mutex
	requests := []protocol.JSONObject{}
	pageGets := 0
	var origin string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/page" {
			mu.Lock()
			pageGets++
			mu.Unlock()
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<h1>Native fixture</h1><p>real bytes</p>")
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			t.Errorf("unexpected real target %s", r.URL.Path)
			http.Error(w, "bad", 404)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var request protocol.JSONObject
		if e := json.Unmarshal(body, &request); e != nil {
			t.Error(e)
		}
		mu.Lock()
		requests = append(requests, request)
		number := len(requests)
		mu.Unlock()
		if string(request.Get("stream")) != "true" {
			t.Errorf("request%d not streaming%s", number, body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch number {
		case 1, 4:
			fmt.Fprint(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(fmt.Sprintf(`{"type":"tool_use","id":"fetch-one","name":"WebFetch","input":{"url":%q,"prompt":"extract heading"}}`, origin+"/page"))}))
		case 2, 5:
			fmt.Fprint(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"Native fixture summary"}`)}))
		case 3, 6:
			if !bytes.Contains(body, []byte("Native fixture summary")) {
				t.Error("main continuation omitted real auxiliary result")
			}
			fmt.Fprint(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"WEBFETCH_GO_OK"}`)}))
		default:
			t.Errorf("unexpected extra model request %d", number)
		}
	}))
	defer server.Close()
	origin = server.URL
	n := nativeForTest(t)
	n.NativeProcess = true
	n.ManagedRoot = filepath.Join(n.Temp, "managed")
	_ = os.MkdirAll(n.ManagedRoot, 0700)
	n.Environment["PATH"] = "/usr/bin:/bin"
	n.Environment["ANTHROPIC_BASE_URL"] = origin
	n.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "deepseek-flash"
	n.Environment["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "0"
	n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("private test has no secure credentials")
	}
	n.ResolvePolicy = func() (*protocol.JSONObject, error) { o := protocol.NewJSONObject(); return &o, nil }
	ca := filepath.Join(n.Temp, "web-ca.pem")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	n.Environment["NODE_EXTRA_CA_CERTS"] = ca
	_ = os.MkdirAll(n.Environment["CLAUDE_CONFIG_DIR"], 0700)
	_ = os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"skipWebFetchPreflight":true}`), 0600)
	disabled := false
	args := PrintArguments{ComputerUse: &disabled, Model: "deepseek-flash", SystemPrompt: "actual shared Web tool fixture", Tools: []string{"WebFetch"}, AllowedTools: []string{"WebFetch"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 8, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}
	var out, stderr bytes.Buffer
	main, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &stderr, OnOutput: func(frame []byte) error { out.Write(frame); return nil }})
	if e != nil {
		t.Fatal(e)
	}
	defer main.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e = main.StartSession(ctx); e != nil {
		t.Fatalf("start actual %v stderr%s", e, stderr.String())
	}
	if e = main.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Fetch page"}}`)); e != nil {
		t.Fatalf("real chain %v stderr%s", e, stderr.String())
	}
	if e = main.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Fetch again using the cache"}}`)); e != nil {
		t.Fatal(e)
	}
	if e = main.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		settings, permission, e := main.Controller.SDKSettingsOnOwner(scope)
		if e != nil {
			return e
		}
		if settings.Get("skipWebFetchPreflight") == nil || permission.String("mode") == "" {
			t.Error("web bindings missing live settings")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = main.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	main.Close()
	mu.Lock()
	count := len(requests)
	gets := pageGets
	var secondary protocol.JSONObject
	if count > 1 {
		secondary = requests[1]
	}
	mu.Unlock()
	if count != 6 || gets != 1 || !strings.Contains(out.String(), "WEBFETCH_GO_OK") {
		t.Fatalf("chain requests%d gets%d output%s stderr%s", count, gets, out.String(), stderr.String())
	}

	transcript := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(n.CWD), args.SessionID+".jsonl")
	history, e := os.ReadFile(transcript)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(history, []byte("Web page content:")) || !bytes.Contains(history, []byte("Native fixture summary")) {
		t.Fatalf("auxiliary prompt leaked or actual result absent from Source transcript %.2000s", history)
	}
	compareActualSourceWebFetchRequest(t, secondary)

	if !strings.Contains(string(secondary.Get("messages")), "Web page content:") || !strings.Contains(string(secondary.Get("messages")), "Native fixture") || secondary.Get("tools") != nil && string(secondary.Get("tools")) != "[]" {
		t.Errorf("auxiliary physical params %s", webRaw(secondary))
	}
}

func compareActualSourceWebFetchRequest(t *testing.T, goRequest protocol.JSONObject) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"Native fixture summary"}`)}))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<h1>Native fixture</h1><p>real bytes</p>")
	}))
	defer server.Close()
	var metadata protocol.JSONObject
	_ = json.Unmarshal(goRequest.Get("metadata"), &metadata)
	var identity protocol.JSONObject
	_ = json.Unmarshal([]byte(metadata.String("user_id")), &identity)
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-webfetch-http.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WEBFETCH_HTTP_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "origin": server.URL, "now": 1770000000000, "modelCalls": true, "sessionId": identity.String("session_id"), "deviceId": identity.String("device_id"), "cases": []map[string]any{{"url": "https://one.fixture:$PORT/page", "call": true, "prompt": "extract heading", "settings": map[string]any{"skipWebFetchPreflight": true}}}})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Actual Source secondary request %v %.2000s", e, stderr.String())
	}
	var source struct {
		Physical     []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Physical) != 1 || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped %v %.2000s", source.Capabilities.Blocked, out)
	}
	var a, b any
	_ = json.Unmarshal(webRaw(goRequest), &a)
	_ = json.Unmarshal(webRaw(source.Physical[0]), &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("physical secondary request Go%s Source%s", webRaw(goRequest), webRaw(source.Physical[0]))
	}
}
