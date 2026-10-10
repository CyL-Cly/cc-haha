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
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeMCPResourceToolsUseRealRegisteredCallsAndSourceOutputs(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var input protocol.JSONObject
		_ = input.UnmarshalJSON(body)
		if input.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result json.RawMessage
		switch input.String("method") {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"resources":{}},"serverInfo":{"name":"private-resource-tools","version":"1"}}`)
		case "resources/list":
			result = []byte(`{"resources":[{"uri":"scheme:text","name":"Text","mimeType":"text/plain","future":{"keep":true}},{"uri":"scheme:binary","name":"Binary"}]}`)
		case "resources/read":
			params, _ := input.Object("params")
			if params.String("uri") == "scheme:binary" {
				result = []byte(`{"contents":[{"uri":"scheme:binary","mimeType":"application/octet-stream","blob":"AAE/Cg=="}]}`)
			} else {
				result = []byte(`{"contents":[{"uri":"scheme:text","mimeType":"text/plain","text":"Actual resource text\n\ud800","future":{"drop":true}}],"future":{"drop":true}}`)
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	rows := []map[string]string{{"name": "ListMcpResourcesTool", "input": `{}`}, {"name": "ListMcpResourcesTool", "input": `{"server":"fixture"}`}, {"name": "ListMcpResourcesTool", "input": `{"server":"missing"}`}, {"name": "ReadMcpResourceTool", "input": `{"server":"fixture","uri":"scheme:text"}`}, {"name": "ReadMcpResourceTool", "input": `{"server":"missing","uri":"scheme:text"}`}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-native-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": f.home, "cwd": f.cwd, "resourceToolCalls": rows, "mcpURL": peer.URL})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %.2600s", e, stderr.String())
	}
	var source struct {
		ResourceToolCalls []protocol.JSONObject
		Capabilities      struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.ResourceToolCalls) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, len(source.ResourceToolCalls), source.Capabilities.Blocked)
	}
	global, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fixture": map[string]string{"type": "http", "url": peer.URL}}})
	f.write(t, filepath.Join(f.config, ".claude.json"), string(global))
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "native resource tool pipeline", Tools: []string{"ListMcpResourcesTool,ReadMcpResourceTool"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, tools, e := m.nativeTools.Catalog(scope)
		if e != nil {
			return e
		}
		catalogue := map[string]queryloop.RegisteredTool{}
		for _, tool := range tools {
			catalogue[tool.Name] = tool
		}
		for i, row := range rows {
			tool, present := catalogue[row["name"]]
			if !present {
				return fmt.Errorf("original connected resource tool absent: %s", row["name"])
			}
			var input protocol.JSONObject
			_ = input.UnmarshalJSON([]byte(row["input"]))
			input, e = tool.ParseInput(input)
			if e != nil {
				return e
			}
			decision, e := m.nativeTools.PermissionDecision(scope, row["name"], &input)
			if e != nil || decision.Behavior != "allow" || !tool.IsConcurrencySafe(input) {
				return fmt.Errorf("original read-only resource permission/concurrency changed: %v/%s", e, decision.Behavior)
			}
			result, failure := tool.CallJSON(ctx, input, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
			actual := protocol.NewJSONObject()
			if failure != nil {
				actual.Set("error", quoteMain(failure.Error()))
			} else {
				mapped, e := tool.MapResultJSON(result.Data, quoteMain("tool-id"))
				if e != nil {
					return e
				}
				actual.Set("data", result.Data)
				actual.Set("mapped", rawMain(mapped))
			}
			if !planSourcesEqual(rawMain(actual), rawMain(source.ResourceToolCalls[i])) {
				return fmt.Errorf("tool%d Go=%s Source=%s", i, rawMain(actual), rawMain(source.ResourceToolCalls[i]))
			}
		}
		var binary protocol.JSONObject
		_ = binary.UnmarshalJSON([]byte(`{"server":"fixture","uri":"scheme:binary"}`))
		result, e := catalogue["ReadMcpResourceTool"].CallJSON(ctx, binary, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
		if e != nil {
			return e
		}
		var parsed struct{ Contents []protocol.JSONObject }
		_ = json.Unmarshal(result.Data, &parsed)
		if len(parsed.Contents) != 1 {
			return fmt.Errorf("binary content lost")
		}
		path := parsed.Contents[0].String("blobSavedTo")
		data, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(data, []byte{0, 1, 63, 10}) || !strings.HasSuffix(path, ".bin") || !strings.Contains(parsed.Contents[0].String("text"), "4 bytes") {
			return fmt.Errorf("actual Source-compatible binary persistence failed: %s/%x/%v", path, data, e)
		}
		if bytes.Contains(result.Data, []byte("blob\"")) {
			return fmt.Errorf("binary base64 leaked into model-visible tool data")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestNativeResourceToolsStayOutOfProductPoolWithoutPeersAndMatchSourceEmptyCalls(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": f.home, "resourceEmpty": true}))
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.2600s", e, output)
	}
	var source struct {
		ResourceEmpty []protocol.JSONObject
		Capabilities  struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.ResourceEmpty) != 2 || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, len(source.ResourceEmpty), source.Capabilities.Blocked)
	}
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "real zero-peer resource calls", Tools: []string{"ListMcpResourcesTool,ReadMcpResourceTool"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e := m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, tools, e := m.nativeTools.Catalog(scope)
		if e != nil {
			return e
		}
		for _, tool := range tools {
			if tool.Name == "ListMcpResourcesTool" || tool.Name == "ReadMcpResourceTool" {
				return fmt.Errorf("unconnected resource helper entered actual product pool")
			}
		}
		for _, expected := range source.ResourceEmpty {
			name := expected.String("name")
			in := protocol.NewJSONObject()
			if name == "ReadMcpResourceTool" {
				_ = in.UnmarshalJSON([]byte(`{"server":"missing","uri":"scheme:actual"}`))
			}
			// Source Tool.call below the product gate still has defined empty
			// semantics. Exercise the actual native handler, not an advertised
			// descriptor or injected session tool.
			data, failure := m.nativeTools.Call(ctx, name, in, scope, false)
			actual := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(name)})
			if failure != nil {
				actual.Set("error", quoteMain(failure.Error()))
			} else {
				mapped, e := builtintools.MapResult(name, data, quoteMain("tool-id"))
				if e != nil {
					return e
				}
				actual.Set("data", data)
				actual.Set("mapped", rawMain(mapped))
			}
			if !planSourcesEqual(rawMain(actual), rawMain(expected)) {
				return fmt.Errorf("zero-peer handler Go=%s Source=%s", rawMain(actual), rawMain(expected))
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
