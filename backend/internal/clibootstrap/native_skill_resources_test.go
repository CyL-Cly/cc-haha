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

func TestSkillResourceMentionsUseSameActualMCPPeerAndOriginalCatalogue(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	var mu sync.Mutex
	methods := []string{}
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
		method := input.String("method")
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		if input.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result json.RawMessage
		switch method {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"resources":{}},"serverInfo":{"name":"private-resource-peer","version":"1"}}`)
		case "resources/list":
			result = []byte(`{"resources":[{"uri":"scheme:first","name":"First","description":"Source metadata","future":{"keep":true}},{"uri":"scheme:binary","name":"Binary"}],"nextCursor":"do-not-follow"}`)
		case "resources/read":
			params, _ := input.Object("params")
			if params.String("uri") == "scheme:binary" {
				result = []byte(`{"contents":[{"uri":"scheme:binary","mimeType":"image/png","blob":"AAAA"}],"future":{"keep":true}}`)
			} else {
				result = []byte(`{"contents":[{"uri":"scheme:first","text":"Actual private MCP resource\n\ud800"}],"future":{"keep":true}}`)
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	inputs := []string{"@fixture:scheme:first @fixture:scheme:first", "@unknown:scheme:first", "@fixture:scheme:missing", "@fixture:scheme:first @fixture:scheme:binary"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-native-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": f.home, "cwd": f.cwd, "resourceMentions": inputs, "mcpURL": peer.URL + "/mcp"})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %.2500s", e, stderr.String())
	}
	var source struct {
		ResourceMentions []json.RawMessage
		Capabilities     struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.ResourceMentions) != len(inputs) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v rows%d blocked%v %.2000s", e, len(source.ResourceMentions), source.Capabilities.Blocked, output)
	}
	mu.Lock()
	methods = nil
	mu.Unlock()
	global, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fixture": map[string]string{"type": "http", "url": peer.URL + "/mcp"}}})
	f.write(t, filepath.Join(f.config, ".claude.json"), string(global))
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "zero model resource consumption", Tools: []string{"Skill,Read"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	for i, text := range inputs {
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			values, e := m.nativeSkillMentions(ctx, scope, text)
			if e != nil {
				return e
			}
			actual := []json.RawMessage{}
			for _, value := range values {
				payload, _ := value.Record.Object("attachment")
				if payload.String("type") == "mcp_resource" {
					actual = append(actual, value.Record.Get("attachment"))
				}
			}
			if !planSourcesEqual(rawMain(actual), source.ResourceMentions[i]) {
				return fmt.Errorf("resource%d Go=%.2000s Source=%.2000s", i, rawMain(actual), source.ResourceMentions[i])
			}
			return nil
		}); e != nil {
			t.Error(e)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	counts := map[string]int{}
	for _, method := range methods {
		counts[method]++
	}
	if counts["initialize"] != 1 || counts["resources/list"] != 1 || counts["resources/read"] != 3 {
		t.Fatal("same peer/catalogue/deduplicated reads were not preserved", counts)
	}
}
