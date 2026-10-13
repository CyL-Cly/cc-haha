package sdkhost_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
	"github.com/NanmiCoder/cc-haha/backend/internal/trajectory"
)

func TestActualDesktopHarnessRequestProducesReadableSystemToolsAndContextSnapshots(t *testing.T) {
	var mu sync.Mutex
	bodies := []json.RawMessage{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"snapshot-response\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"actual snapshot request\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer peer.Close()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home, work := filepath.Join(root, "home"), filepath.Join(root, "work")
	config := filepath.Join(home, ".claude")
	for _, p := range []string{home, work, config} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config, "ANTHROPIC_BASE_URL": peer.URL, "ANTHROPIC_API_KEY": "fake-snapshot-key", "CLAUDE_CODE_ENTRYPOINT": "claude-desktop", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "PATH": "/usr/bin:/bin", "TZ": "UTC", "ANTHROPIC_DEFAULT_SONNET_MODEL": "fixture-model"}
	native, e := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: work, Platform: "darwin", ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	trace, e := tracecapture.New(tracecapture.Options{Scope: config, ResolveMode: func() string { return "off" }, Env: func(key string) string { return env[key] }})
	if e != nil {
		t.Fatal(e)
	}
	defer trace.Close(context.Background())
	sid := "6b37c9bc-d58a-45e0-8f58-8f93d3ef822f"
	process, e := clibootstrap.NewMainPrint(clibootstrap.MainPrintOptions{Trace: trace, Native: native, PID: os.Getpid(), Version: "999.0.0-snapshot", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: clibootstrap.PrintArguments{SessionID: sid, Model: "sonnet", SystemPrompt: "physical request snapshot marker", Tools: []string{"Read,Write,Bash"}, PermissionMode: "bypassPermissions", MaxTurns: 2, Sources: clibootstrap.DefaultState()}})
	if e != nil {
		t.Fatal(e)
	}
	defer process.Close()
	if e = process.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: []byte(`"user"`)}, protocol.JSONField{Name: "message", Value: []byte(`{"role":"user","content":"verify physical snapshot"}`)})
	if e = process.RunTurn(context.Background(), user); e != nil {
		t.Fatal(e)
	}
	if e = trace.Drain(context.Background()); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(config, "projects", projectpath.Sanitize(work), sid, "prompt-snapshots.jsonl")
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(data), "fake-snapshot-key") {
		t.Fatal("snapshot captured auth")
	}
	mu.Lock()
	captured := append([]json.RawMessage{}, bodies...)
	mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("actual physical requests: %d", len(captured))
	}
	var body map[string]any
	json.Unmarshal(captured[0], &body)
	service := trajectory.New(sessionstore.NewReader(nil))
	transcript := filepath.Join(filepath.Dir(filepath.Dir(path)), sid+".jsonl")
	page, e := service.Page(context.Background(), trajectory.Source{Path: transcript, SnapshotPath: path, Scope: "main", Main: true}, trajectory.PageOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if page.Snapshots == nil || len(*page.Snapshots) < 2 {
		t.Fatalf("no actual request/context rows: %s", data)
	}
	var ref map[string]any
	for _, r := range *page.Snapshots {
		if r["kind"] == "system" {
			ref = r["snapshot"].(map[string]any)
		}
	}
	if ref == nil || ref["model"] != body["model"] || int(ref["toolCount"].(float64)) != len(body["tools"].([]any)) {
		t.Fatalf("snapshot does not match physical request: %v", ref)
	}
	blob, e := service.Snapshot(context.Background(), path, ref["tools"].(string))
	if e != nil {
		t.Fatal(e)
	}
	if len(blob["json"].([]any)) != len(body["tools"].([]any)) {
		t.Fatalf("wire tools absent: %v", blob)
	}
	sys, e := service.Snapshot(context.Background(), path, ref["sys"].(string))
	if e != nil || !strings.Contains(sys["text"].(string), "physical request snapshot marker") {
		t.Fatal(sys, e)
	}
}
