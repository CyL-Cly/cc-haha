package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeResourcePrefetchStartsAllPeersBeforeAwaitAndPreservesSourceOrder(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var mu sync.Mutex
	arrivals := map[string]bool{}
	counts := map[string]int{}
	allStarted := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var input protocol.JSONObject
		data, _ := io.ReadAll(r.Body)
		if e := input.UnmarshalJSON(data); e != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if input.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		method := input.String("method")
		mu.Lock()
		counts[name+"/"+method]++
		mu.Unlock()
		var result json.RawMessage
		switch method {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"resources":{}},"serverInfo":{"name":"private-parallel-peer","version":"1"}}`)
		case "resources/list":
			if name == "bad" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "error", Value: []byte(`{"code":-32603,"message":"original per-peer failure"}`)}))
				return
			}
			mu.Lock()
			arrivals[name] = true
			if len(arrivals) == 2 {
				close(allStarted)
			}
			barrier := allStarted
			mu.Unlock()
			// No timer or synthetic scheduler: each real HTTP peer refuses to
			// settle until the other original resource request has started.
			select {
			case <-barrier:
			case <-ctx.Done():
				return
			}
			result = rawMain(map[string]any{"resources": []any{map[string]string{"uri": "scheme:" + name, "name": name}}})
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	t.Cleanup(peer.Close)
	names := []string{"omega", "alpha", "bad"}
	servers := protocol.NewJSONObject()
	configs := []map[string]string{}
	for _, name := range names {
		config := map[string]string{"type": "http", "url": peer.URL + "/" + name}
		servers.Set(name, rawMain(config))
		configs = append(configs, map[string]string{"name": name, "url": peer.URL + "/" + name})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-native-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	data := rawMain(map[string]any{"homeDir": f.home, "cwd": f.cwd, "resourceParallel": configs, "mcpURL": peer.URL})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.2500s", e, output)
	}
	var source struct {
		ResourceParallel json.RawMessage
		Capabilities     struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.ResourceParallel) == 0 || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v blocked=%v %.2000s", e, source.Capabilities.Blocked, output)
	}
	mu.Lock()
	arrivals, counts, allStarted = map[string]bool{}, map[string]int{}, make(chan struct{})
	mu.Unlock()
	f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "mcpServers", Value: rawMain(servers)}))))
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual parallel resource fixture", Tools: []string{"ListMcpResourcesTool,ReadMcpResourceTool"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e := m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		actual, e := m.mcpTools.ListResourceTool(ctx, scope, nil)
		if e != nil {
			return e
		}
		if !planSourcesEqual(actual, source.ResourceParallel) {
			return fmt.Errorf("Source result order/per-peer error changed: Go=%s Source=%s", actual, source.ResourceParallel)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, name := range names {
		if counts[name+"/initialize"] != 1 || counts[name+"/resources/list"] != 1 {
			t.Error("catalogue did not remain warm on same actual connection", name, counts)
		}
	}
}
