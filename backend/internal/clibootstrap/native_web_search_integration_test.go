package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
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

func TestSharedMainNativeWebSearchProgressTranscriptAndPhysicalRequests(t *testing.T) {
	var mu sync.Mutex
	requests := []protocol.JSONObject{}
	model := "claude-sonnet-4-5-20250929"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request protocol.JSONObject
		_ = json.Unmarshal(body, &request)
		mu.Lock()
		requests = append(requests, request)
		number := len(requests)
		mu.Unlock()
		if string(request.Get("stream")) != "true" {
			t.Error("native request not streamed")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch number {
		case 1:
			fmt.Fprint(w, webWire(model, []protocol.JSONObject{webObject(`{"type":"tool_use","id":"outer-search","name":"WebSearch","input":{"query":"Go docs","allowed_domains":["go.dev"]}}`)}))
		case 2:
			if !bytes.Contains(body, []byte(`"type":"web_search_20250305"`)) || !bytes.Contains(body, []byte("Perform a web search for the query: Go docs")) {
				t.Errorf("native schema/prompt missing %.2000s", body)
			}
			fmt.Fprint(w, webWire(model, []protocol.JSONObject{webObject(`{"type":"server_tool_use","id":"native-search-id","name":"web_search","input":{"query":"Go current docs"}}`), webObject(`{"type":"web_search_tool_result","tool_use_id":"native-search-id","content":[{"type":"web_search_result","title":"Go docs","url":"https://go.dev/doc","encrypted_content":"opaque"}]}`), webObject(`{"type":"text","text":"Search summary"}`)}))
		case 3:
			if !bytes.Contains(body, []byte("https://go.dev/doc")) || !bytes.Contains(body, []byte("REMINDER: You MUST include")) {
				t.Error("real links absent from model continuation")
			}
			if bytes.Contains(body, []byte("query_update")) || bytes.Contains(body, []byte("search_results_received")) {
				t.Error("progress leaked into model payload")
			}
			fmt.Fprint(w, webWire(model, []protocol.JSONObject{webObject(`{"type":"text","text":"WEBSEARCH_GO_OK"}`)}))
		default:
			t.Errorf("unexpected model request%d", number)
		}
	}))
	defer server.Close()
	n := nativeForTest(t)
	n.NativeProcess = true
	n.ManagedRoot = filepath.Join(n.Temp, "managed")
	_ = os.MkdirAll(n.ManagedRoot, 0700)
	n.Environment["PATH"] = "/usr/bin:/bin"
	n.Environment["ANTHROPIC_BASE_URL"] = server.URL
	n.Environment["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "0"
	n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("private fixture no secure credential")
	}
	n.ResolvePolicy = func() (*protocol.JSONObject, error) { o := protocol.NewJSONObject(); return &o, nil }
	_ = os.MkdirAll(n.Environment["CLAUDE_CONFIG_DIR"], 0700)
	_ = os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"webSearch":{"mode":"anthropic"}}`), 0600)
	disabled := false
	args := PrintArguments{ComputerUse: &disabled, Model: model, SystemPrompt: "actual WebSearch fixture", Tools: []string{"WebSearch"}, AllowedTools: []string{"WebSearch"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 8, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}
	var frames []protocol.JSONObject
	var yields []protocol.JSONObject
	main, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func(frame []byte) error {
		var o protocol.JSONObject
		_ = json.Unmarshal(frame, &o)
		frames = append(frames, o)
		return nil
	}, OnQueryYield: func(scope *cooperative.Scope, yield *queryloop.Yield) error {
		if yield.Message != nil && yield.Message.Type() == "progress" {
			yields = append(yields, yield.Message.Record.Clone())
		}
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer main.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e = main.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	if e = main.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Find Go docs"}}`)); e != nil {
		t.Fatal(e)
	}
	if e = main.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	count := len(requests)
	var nativeRequest protocol.JSONObject
	if count > 1 {
		nativeRequest = requests[1]
	}
	mu.Unlock()
	if count != 3 {
		t.Fatalf("expected main/native/main requests got%d frames%s", count, webRaw(frames))
	}
	compareSourceNativeWebSearchRequest(t, nativeRequest, yields)
	if len(yields) != 2 {
		t.Fatalf("actual progress missing count%d %s", len(yields), webRaw(yields))
	}
	for _, p := range yields {
		if p.String("parentToolUseID") != "outer-search" || p.String("uuid") == "" || p.String("timestamp") == "" {
			t.Errorf("progress envelope lost Source identity%s", webRaw(p))
		}
	}
	for _, frame := range frames {
		if frame.String("type") == "tool_progress" {
			t.Error("Source SDK should ignore Web progress")
		}
		if frame.String("type") == "result" && string(frame.Get("num_turns")) != "2" {
			t.Errorf("auxiliary search counted as parent model turn%s", webRaw(frame))
		}
	}
	path := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(n.CWD), args.SessionID+".jsonl")
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(data, []byte("query_update")) || bytes.Contains(data, []byte("search_results_received")) || bytes.Contains(data, []byte("Perform a web search for the query:")) {
		t.Errorf("Source progress persist/auxiliary transcript differs %.3000s", data)
	}
}

func compareSourceNativeWebSearchRequest(t *testing.T, goRequest protocol.JSONObject, progress []protocol.JSONObject) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		return
	}
	model := "claude-sonnet-4-5-20250929"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, webWire(model, []protocol.JSONObject{webObject(`{"type":"server_tool_use","id":"native-search-id","name":"web_search","input":{"query":"Go current docs"}}`), webObject(`{"type":"web_search_tool_result","tool_use_id":"native-search-id","content":[{"type":"web_search_result","title":"Go docs","url":"https://go.dev/doc","encrypted_content":"opaque"}]}`), webObject(`{"type":"text","text":"Search summary"}`)}))
	}))
	defer server.Close()
	var metadata, identity protocol.JSONObject
	_ = json.Unmarshal(goRequest.Get("metadata"), &metadata)
	_ = json.Unmarshal([]byte(metadata.String("user_id")), &identity)
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-websearch.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WEBSEARCH_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "origin": server.URL, "model": model, "native": map[string]any{"query": "Go docs", "allowed_domains": []string{"go.dev"}}, "sessionId": identity.String("session_id"), "deviceId": identity.String("device_id"), "progressMessages": progress})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source native search %v %.3000s", e, stderr.String())
	}
	var source struct {
		Physical       []protocol.JSONObject
		NativeProgress []protocol.JSONObject
		ProgressClean  [][]json.RawMessage
		Capabilities   struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Physical) != 1 || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped%v %.2400s stderr%.1000s", source.Capabilities.Blocked, out, stderr.String())
	}
	var a, b any
	_ = json.Unmarshal(webRaw(goRequest), &a)
	_ = json.Unmarshal(webRaw(source.Physical[0]), &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("native physical body Go%s Source%s", webRaw(goRequest), webRaw(source.Physical[0]))
	}
	if len(progress) != len(source.NativeProgress) {
		t.Fatalf("native progress Go%d Source%d", len(progress), len(source.NativeProgress))
	}
	for i, p := range progress {
		plain := protocol.NewJSONObject(protocol.JSONField{Name: "toolUseID", Value: p.Get("toolUseID")}, protocol.JSONField{Name: "data", Value: p.Get("data")})
		_ = json.Unmarshal(webRaw(plain), &a)
		_ = json.Unmarshal(webRaw(source.NativeProgress[i]), &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("progress%d Go%s Source%s", i, webRaw(plain), webRaw(source.NativeProgress[i]))
		}
		if len(source.ProgressClean[i]) != 0 {
			t.Fatal("Source disk progress policy changed")
		}
	}
}
