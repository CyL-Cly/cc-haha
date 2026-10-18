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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

type actualPromptPeer struct {
	server                   *httptest.Server
	lists                    atomic.Int32
	toolLists                atomic.Int32
	version                  atomic.Int32
	mu                       sync.Mutex
	arguments                []protocol.JSONObject
	notifications            chan string
	streamReady              chan struct{}
	streamOnce               sync.Once
	getStarted, getCancelled chan struct{}
	protocolCancelled        chan struct{}
}

func newActualPromptPeer(t *testing.T) *actualPromptPeer {
	t.Helper()
	p := &actualPromptPeer{notifications: make(chan string, 4), streamReady: make(chan struct{}), getStarted: make(chan struct{}, 1), getCancelled: make(chan struct{}, 1), protocolCancelled: make(chan struct{}, 1)}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			p.streamOnce.Do(func() { close(p.streamReady) })
			for {
				select {
				case method := <-p.notifications:
					fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":%q,\"params\":{}}\n\n", method)
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(data)
		if message.Get("id") == nil {
			if message.String("method") == "notifications/cancelled" {
				params, _ := message.Object("params")
				if params.Get("requestId") == nil {
					t.Error("actual protocol cancellation lacks request ownership", params)
				}
				p.protocolCancelled <- struct{}{}
			}
			w.WriteHeader(202)
			return
		}
		var result json.RawMessage
		switch message.String("method") {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "private-prompt-session")
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"prompts":{"listChanged":true},"tools":{"listChanged":true}},"serverInfo":{"name":"actual-prompt-peer","version":"1"}}`)
		case "tools/list":
			p.toolLists.Add(1)
			name := "original_tool"
			if p.version.Load() > 0 {
				name = "updated_tool"
			}
			result = rawMain(map[string]any{"tools": []any{map[string]any{"name": name, "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}})
		case "prompts/list":
			p.lists.Add(1)
			name := "review.file"
			if p.version.Load() > 0 {
				name = "updated.file"
			}
			result = rawMain(map[string]any{"prompts": []any{map[string]any{"name": name, "title": "Display title has spaces", "description": "Original\u202E prompt", "arguments": []any{map[string]any{"name": "file", "required": true, "future": "strip"}, map[string]any{"name": "mode"}, map[string]any{"name": "third"}}, "future": "strip"}}, "nextCursor": "must-not-follow", "future": "strip"})
		case "prompts/get":
			params, _ := message.Object("params")
			arguments, _ := params.Object("arguments")
			p.mu.Lock()
			p.arguments = append(p.arguments, arguments)
			p.mu.Unlock()
			if arguments.String("file") == "cancel" {
				p.getStarted <- struct{}{}
				select {
				case <-p.protocolCancelled:
				case <-r.Context().Done():
				}
				p.getCancelled <- struct{}{}
				return
			}
			result = []byte(`{"description":"ignored envelope","messages":[{"role":"assistant","content":{"type":"text","text":"actual original prompt text","future":"strip"}},{"role":"user","content":{"type":"resource","resource":{"uri":"owned:text","text":"resource prompt"}}},{"role":"assistant","content":{"type":"resource_link","name":"link","uri":"owned:link","description":"detail"}}]}`)
		default:
			t.Error("unexpected actual prompt MCP method", message)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	t.Cleanup(p.server.Close)
	return p
}

func newPromptMain(t *testing.T, f nativePluginFixture, peer *actualPromptPeer, interactive bool, changed func([]sdkhost.ControlCommand) error) *MainPrint {
	t.Helper()
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-prompt-model"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	configs := rawMain(map[string]any{"mcpServers": map[string]any{"owned-peer": map[string]string{"type": "http", "url": peer.server.URL}}})
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Interactive: interactive, OnCommandsChanged: changed, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{MCPConfigs: []string{string(configs)}, ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual MCP prompt pipeline", Tools: []string{""}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}

func TestNativeMCPPromptCommandsActualSourceMetadataArgumentsAndContent(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	f := newNativePluginFixture(t)
	peer := newActualPromptPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-mcp-prompt-commands.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_MCP_PROMPT_COMMANDS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=private-source-fixture", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	args := []string{"alpha  beta ignored", "alpha", ""}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"cwd": f.n.CWD, "url": peer.server.URL, "name": "owned-peer", "args": args}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source prompt producer: %v %.2500s", err, stderr.String())
	}
	var source struct {
		Metadata                    []protocol.JSONObject
		CachedCount, RefreshedCount int
		Values                      [][]protocol.JSONObject
	}
	if err := json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if len(source.Metadata) != 1 || source.CachedCount != 1 || source.RefreshedCount != 1 || peer.lists.Load() != 2 {
		t.Fatal("Source single page/cache contract changed", string(output), peer.lists.Load())
	}
	peer.mu.Lock()
	sourceArgs := append([]protocol.JSONObject{}, peer.arguments...)
	peer.arguments = nil
	peer.mu.Unlock()
	peer.lists.Store(0)
	m := newPromptMain(t, f, peer, false, nil)
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		commands, err := m.mcpTools.PromptCommands(scope)
		if err != nil {
			return err
		}
		if len(commands) != 1 {
			t.Fatalf("actual MCP prompt inventory not admitted: %d", len(commands))
		}
		mounted, ok := m.Registry.Prompt(commands[0].UserFacingName())
		if !ok || mounted.Name() != commands[0].Name() {
			t.Fatal("completed headless discovery was not published to the initial actual slash registry", commands[0].UserFacingName())
		}
		if !bytes.Equal(rawMain(commands[0].Metadata()), rawMain(source.Metadata[0])) {
			// Compare decoded semantic objects: JSON schema insertion order is
			// observable on tool requests, while command metadata is object data.
			var got, want any
			_ = json.Unmarshal(rawMain(commands[0].Metadata()), &got)
			_ = json.Unmarshal(rawMain(source.Metadata[0]), &want)
			if !bytes.Equal(rawMain(got), rawMain(want)) {
				t.Fatalf("Source prompt metadata differs\nGo %s\nSource %s", rawMain(got), rawMain(want))
			}
		}
		inv, err := m.nativeSkillInvocation(scope)
		if err != nil {
			return err
		}
		for index, args := range args {
			blocks, err := commands[0].PromptBlocks(ctx, &args, inv)
			if err != nil {
				return err
			}
			if !bytes.Equal(rawMain(blocks), rawMain(source.Values[index])) {
				t.Fatalf("Source prompt transformed content differs\nGo %s\nSource %s", rawMain(blocks), rawMain(source.Values[index]))
			}
		}
		_, err = m.mcpTools.PromptCommands(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.lists.Load() != 1 || !bytes.Equal(rawMain(peer.arguments), rawMain(sourceArgs)) {
		t.Fatal("actual cached prompt list/literal-space zip differs", peer.lists.Load(), string(rawMain(peer.arguments)), string(rawMain(sourceArgs)))
	}
}

func TestInteractiveMCPPromptChangeRealSlashToSameModelAndCancel(t *testing.T) {
	f := newNativePluginFixture(t)
	peer := newActualPromptPeer(t)
	var models atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		models.Add(1)
		if !bytes.Contains(data, []byte("actual original prompt text")) || !bytes.Contains(data, []byte("[Resource from owned-peer at owned:text] resource prompt")) || !bytes.Contains(data, []byte("[Resource link: link] owned:link (detail)")) || !bytes.Contains(data, []byte("<command-name>/mcp__owned-peer__updated.file</command-name>")) || !bytes.Contains(data, []byte(`"name":"mcp__owned-peer__updated_tool"`)) || bytes.Contains(data, []byte(`"name":"mcp__owned-peer__original_tool"`)) {
			t.Error("actual MCP slash transformed blocks/loading never reached canonical model POST", string(data))
		}
		emitPlanFixture(w, 1, "", "Actual MCP command complete")
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	changed := make(chan []sdkhost.ControlCommand, 12)
	m := newPromptMain(t, f, peer, true, func(commands []sdkhost.ControlCommand) error {
		select {
		case changed <- commands:
		default:
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	waitCommand := func(name string) {
		t.Helper()
		for {
			select {
			case commands := <-changed:
				for _, command := range commands {
					if command.Name == name {
						return
					}
				}
			case <-ctx.Done():
				t.Fatal("actual MCP catalog change not published", name, ctx.Err())
			}
		}
	}
	waitCommand("owned-peer:review.file (MCP)")
	select {
	case <-peer.streamReady:
	case <-ctx.Done():
		t.Fatal("physical notification stream missing")
	}
	peer.version.Store(1)
	peer.notifications <- "notifications/prompts/list_changed"
	waitCommand("owned-peer:updated.file (MCP)")
	peer.notifications <- "notifications/tools/list_changed"
	for {
		ready := false
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			definitions, _, err := m.mcpTools.Catalog(scope)
			for _, definition := range definitions {
				ready = ready || definition.Name == "mcp__owned-peer__updated_tool"
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		commands, err := m.mcpTools.PromptCommands(scope)
		if len(commands) != 1 || commands[0].Name() != "mcp__owned-peer__updated.file" {
			t.Error("old prompt retained after list_changed", commands)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(text)}))})
	}
	if err := m.RunTurn(ctx, user("/owned-peer:updated.file (MCP) alpha  beta extra")); err != nil {
		t.Fatal(err)
	}
	if models.Load() != 1 || peer.lists.Load() != 2 {
		t.Fatal("prompt invocation listed again/changed model owner", models.Load(), peer.lists.Load())
	}
	if peer.toolLists.Load() != 2 {
		t.Error("tools/list_changed refreshed prompt cache or reused stale tool metadata", peer.toolLists.Load())
	}
	turnCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- m.RunTurn(turnCtx, user("/mcp__owned-peer__updated.file cancel")) }()
	select {
	case <-peer.getStarted:
	case <-ctx.Done():
		t.Fatal("cancel fixture did not reach physical prompts/get")
	}
	stop()
	select {
	case err := <-done:
		if err == nil {
			t.Error("cancelled prompt invocation succeeded")
		}
	case <-ctx.Done():
		t.Fatal("prompt cancellation blocked owned turn")
	}
	select {
	case <-peer.getCancelled:
	case <-ctx.Done():
		t.Fatal("prompt cancellation did not stop actual MCP request producer")
	}
	if models.Load() != 1 {
		t.Error("cancelled prompt leaked to inference", models.Load())
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
