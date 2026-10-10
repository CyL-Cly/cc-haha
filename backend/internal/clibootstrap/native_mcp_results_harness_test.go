package clibootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualDefaultMainPrintGenericMCPImageAudioAndBlobReachModelAndPhysicalRead(t *testing.T) {
	actualMainPrintGenericMCPRich(t, "")
}
func TestActualDefaultMainPrintGenericMCPOfficialLive(t *testing.T) {
	token := os.Getenv("CC_HAHA_MCP_RICH_LIVE_TOKEN")
	if token == "" {
		t.Skip("separate maintainer-authorized official model evidence")
	}
	if os.Getenv("CC_HAHA_MCP_RICH_LIVE_GATE") != "1" {
		t.Fatal("unowned live model gate")
	}
	actualMainPrintGenericMCPRich(t, token)
}
func TestActualGenericMCPOrdinaryDefaultsPermissionAndPrivateReceipt(t *testing.T) {
	actualMainPrintGenericMCPRich(t, "", true)
}
func actualMainPrintGenericMCPRich(t *testing.T, liveToken string, defaultProfile ...bool) {
	ordinary := liveToken != "" || len(defaultProfile) > 0 && defaultProfile[0]
	var f nativePluginFixture
	var options *NativeComputerUseOptions
	if ordinary {
		f = newNativePluginFixture(t)
		f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
		f.n.Environment["ANTHROPIC_API_KEY"] = "private-mcp-default-fake-key"
		f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	} else {
		f, _, options = computerUseMainFixture(t)
	}
	receiptRoot := ""
	if ordinary {
		receiptRoot = t.TempDir()
	}
	if liveToken != "" {
		receiptRoot = os.Getenv("CC_HAHA_MCP_RICH_LIVE_RECEIPT_ROOT")
		if !filepath.IsAbs(receiptRoot) {
			t.Fatal("live receipt root must be explicit absolute private directory")
		}
		real, e := filepath.EvalSymlinks(receiptRoot)
		if e != nil {
			t.Fatal(e)
		}
		temporary, _ := filepath.EvalSymlinks(os.TempDir())
		if !inside(temporary, real) && !inside("/private/tmp", real) {
			t.Fatal("live receipt root must be disposable")
		}
		receiptRoot = real
	}

	var encoded bytes.Buffer
	if e := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 12, 8))); e != nil {
		t.Fatal(e)
	}
	imageData := base64.StdEncoding.EncodeToString(encoded.Bytes())
	binaryData := []byte("MCP_RICH_PHYSICAL_FILE\n")
	if liveToken != "" && os.Getenv("CC_HAHA_MCP_RICH_LIVE_IMAGE") != "1" {
		imageData = ""
	}
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	var mu sync.Mutex
	counts := map[string]int{}
	requests := []protocol.JSONObject{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var input protocol.JSONObject
		data, _ := io.ReadAll(r.Body)
		_ = input.UnmarshalJSON(data)
		if input.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		mu.Lock()
		counts[input.String("method")]++
		mu.Unlock()
		var result json.RawMessage
		switch input.String("method") {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"private-resource-harness","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"rich","description":"private rich media","inputSchema":{"type":"object","properties":{}},"annotations":{"readOnlyHint":true}}]}`)
		case "tools/call":
			content := []any{map[string]any{"type": "audio", "data": "AAE/Cg==", "mimeType": "audio/wav"}, map[string]any{"type": "resource", "resource": map[string]any{"uri": "owned:///private", "mimeType": "text/plain", "blob": base64.StdEncoding.EncodeToString(binaryData)}}}
			if imageData != "" {
				content = append([]any{map[string]any{"type": "image", "data": imageData, "mimeType": "image/png"}}, content...)
			}
			result = rawMain(map[string]any{"content": content, "structuredContent": map[string]bool{"actual": true}})
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	t.Cleanup(peer.Close)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var input protocol.JSONObject
		_ = input.UnmarshalJSON(data)
		mu.Lock()
		requests = append(requests, input)
		index := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(event string, value any) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, rawMain(value)) }
		emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("resources-%d", index), "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}})
		stop := "end_turn"
		readPath := ""
		if index == 2 {
			var body struct{ Messages []protocol.JSONObject }
			json.Unmarshal(data, &body)
			for _, message := range body.Messages {
				var blocks []protocol.JSONObject
				json.Unmarshal(message.Get("content"), &blocks)
				for _, block := range blocks {
					if block.String("type") != "tool_result" {
						continue
					}
					var contents []protocol.JSONObject
					json.Unmarshal(block.Get("content"), &contents)
					for _, content := range contents {
						text := content.String("text")
						if strings.HasPrefix(text, "[Resource from fixture at owned:///private]") {
							parts := strings.Split(text, " saved to ")
							if len(parts) == 2 {
								readPath = parts[1]
							}
						}
					}
				}
			}
		}
		if index == 1 || index == 2 && readPath != "" {
			calls := []struct{ name, id, input string }{{"mcp__fixture__rich", "rich-result", `{}`}}
			if index == 2 {
				calls = []struct{ name, id, input string }{{"Read", "read-saved-blob", string(rawMain(map[string]string{"file_path": readPath}))}}
			}
			for i, call := range calls {
				emit("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": call.id, "name": call.name, "input": map[string]any{}}})
				emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]string{"type": "input_json_delta", "partial_json": call.input}})
				emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
			}
			stop = "tool_use"
		} else {
			emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": "Actual MCP resources processed"}})
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		}
		emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}})
		emit("message_stop", map[string]string{"type": "message_stop"})
	}))
	t.Cleanup(model.Close)
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	modelID := "fixture-model"
	if liveToken != "" {
		delete(f.n.Environment, "ANTHROPIC_BASE_URL")
		delete(f.n.Environment, "ANTHROPIC_API_KEY")
		modelID = os.Getenv("CC_HAHA_MCP_RICH_LIVE_MODEL")
		if modelID == "" {
			modelID = "deepseek-flash[1m]"
		}
		base := os.Getenv("CC_HAHA_MCP_RICH_LIVE_BASE_URL")
		if base == "" {
			base = "https://api.deepseek.com/anthropic"
		}
		provider := map[string]any{"id": "private-mcp-rich-live", "presetId": "deepseek", "name": "Private MCP rich official validation", "baseUrl": base, "apiKey": liveToken, "authStrategy": "auth_token", "apiFormat": "anthropic", "supportsNestedToolResultMedia": false, "toolSearchEnabled": false, "models": map[string]string{"main": modelID, "haiku": modelID, "sonnet": modelID, "opus": modelID}, "modelContextWindows": map[string]int{"deepseek-flash": 1000000}, "autoCompactWindow": 1000000}
		f.write(t, filepath.Join(f.config, "cc-haha", "providers.json"), string(rawMain(map[string]any{"schemaVersion": 6, "activeId": "private-mcp-rich-live", "providers": []any{provider}, "providerOrder": []string{"private-mcp-rich-live"}})))
	}

	f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(map[string]any{"mcpServers": map[string]any{"fixture": map[string]string{"type": "http", "url": peer.URL}}})))
	disabled := false
	arguments := PrintArguments{ComputerUse: &disabled, Model: modelID, SystemPrompt: "Use the actual tools to execute the requested task.", Tools: []string{"mcp__fixture__rich,Read"}, PermissionMode: "bypassPermissions", MaxTurns: 8, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}
	if ordinary {
		arguments.Tools = nil
		arguments.SystemPrompt = ""
		arguments.PermissionMode = "default"
		arguments.ComputerUse = nil
		arguments.Thinking = requestcontext.Thinking{}
		f.n.Environment["CC_HAHA_TRACE_API_CALLS"] = "1"
	}
	var m *MainPrint
	var e error
	var approvals atomic.Int32
	resolve := func(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
		if !ordinary || call.PermDecision == nil || call.PermDecision.Behavior != "ask" {
			return queryloop.ToolPermission{}, fmt.Errorf("actual default ASK descriptor missing")
		}
		permission, e := m.Host.NativePermissionContext(scope)
		if e != nil {
			return queryloop.ToolPermission{}, e
		}
		if permission.String("mode") != "default" {
			return queryloop.ToolPermission{}, fmt.Errorf("ordinary permission mode was changed")
		}
		if tool.Name != "mcp__fixture__rich" && tool.Name != "Read" {
			return queryloop.ToolPermission{Behavior: "deny", Input: input}, nil
		}
		future, e := ownerasync.NewOwnerFuture[queryloop.ToolPermission](m.Owner)
		if e != nil {
			return queryloop.ToolPermission{}, e
		}
		approvals.Add(1)
		receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(next *cooperative.Scope) error {
			return future.Resolve(next, queryloop.ToolPermission{Behavior: "allow", Input: input, UpdatedInput: input})
		})
		if e != nil {
			return queryloop.ToolPermission{}, e
		}
		_ = receipt
		return future.Await(scope)
	}
	printOptions := MainPrintOptions{ComputerUse: options, Arguments: arguments, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard}
	if ordinary {
		printOptions.ResolvePermission = resolve
	}
	m, e = NewMainPrint(printOptions)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	if e := m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	prompt := "List and read the actual private MCP resource"
	if liveToken != "" {
		prompt = "Call mcp__fixture__rich exactly once. It returns an audio file path and a text resource file path. Use Read on the saved text resource file to verify its contents, then reply with MCP_RICH_LIVE_DONE. Do not use Bash or delegate. The audio is only to test exact binary persistence; do not interpret audio or claim image understanding."
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(prompt)}))})
	if e := m.RunTurn(ctx, user); e != nil {
		t.Fatal(e)
	}
	if liveToken != "" {
		mu.Lock()
		callCount := counts["tools/call"]
		mu.Unlock()
		if len(requests) != 0 {
			t.Fatal("official live branch used fake model transport")
		}
		if callCount != 1 {
			t.Fatal("official agent did not call actual MCP exactly once", callCount)
		}
		var records []protocol.JSONObject
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			original, err := m.Host.OriginalHistoryOnOwner(scope)
			if err != nil {
				return err
			}
			for _, record := range original {
				data, err := record.MarshalJSON()
				if err != nil {
					return err
				}
				var value protocol.JSONObject
				if err = value.UnmarshalJSON(data); err != nil {
					return err
				}
				records = append(records, value)
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		history := rawMain(records)
		debugRoot := os.Getenv("CC_HAHA_MCP_RICH_LIVE_RECEIPT_ROOT")
		if filepath.IsAbs(debugRoot) {
			if e := os.WriteFile(filepath.Join(debugRoot, "failure-history-private.json"), history, 0600); e != nil {
				t.Fatal(e)
			}
		}
		readIDs := map[string]bool{}
		readResult := false
		final := false
		for _, record := range records {
			message, _ := record.Object("message")
			var blocks []protocol.JSONObject
			json.Unmarshal(message.Get("content"), &blocks)
			for _, block := range blocks {
				if block.String("type") == "tool_use" && block.String("name") == "Read" {
					readIDs[block.String("id")] = true
				}
				if block.String("type") == "tool_result" && readIDs[block.String("tool_use_id")] && bytes.Contains(rawMain(block), binaryData[:len(binaryData)-1]) {
					readResult = true
				}
				if block.String("type") == "text" && block.String("text") == "MCP_RICH_LIVE_DONE" && record.String("type") == "assistant" {
					final = true
				}
			}
		}
		if !readResult || !final || !bytes.Contains(history, binaryData[:len(binaryData)-1]) {
			t.Fatalf("official model did not complete actual Read tool/result and final response (read=%t final=%t ReadIDs=%d)", readResult, final, len(readIDs))
		}
		if bytes.Contains(history, []byte(liveToken)) {
			t.Fatal("official key entered canonical history")
		}
		directory := ""
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			directory, e = m.Controller.ToolResultsDirectoryOnOwner(scope)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		files, e := filepath.Glob(filepath.Join(directory, "mcp-fixture-blob-*"))
		if e != nil || len(files) != 2 {
			t.Fatal("official path did not create two actual binary files", e, len(files))
		}
		for _, path := range files {
			p, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			if strings.HasSuffix(path, ".txt") && !bytes.Equal(p, binaryData) {
				t.Fatal("official Read text resource bytes changed")
			}
			if strings.HasSuffix(path, ".wav") && !bytes.Equal(p, []byte{0, 1, 63, 10}) {
				t.Fatal("official audio persistence bytes changed")
			}
		}
		persistMCPRichPrivateReceipt(t, m, receiptRoot, files, history, liveToken, callCount, approvals.Load(), imageData != "", true)
		t.Logf("official shared MainPrint completed SID=%s MCPCalls=%d persistedFiles=%d imageRequested=%t", m.Controller.SessionID(), callCount, len(files), imageData != "")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 || counts["initialize"] != 1 || counts["tools/list"] != 1 || counts["tools/call"] != 1 {
		t.Fatal("actual shared rich result did not reach subsequent Read/model consumer", len(requests), counts)
	}
	var body struct{ Messages []protocol.JSONObject }
	_ = json.Unmarshal(rawMain(requests[1]), &body)
	imageSeen := false
	paths := []string{}
	for _, message := range body.Messages {
		var blocks []protocol.JSONObject
		json.Unmarshal(message.Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("tool_use_id") != "rich-result" {
				continue
			}
			var content []protocol.JSONObject
			json.Unmarshal(block.Get("content"), &content)
			for _, item := range content {
				if item.String("type") == "image" {
					source, _ := item.Object("source")
					imageSeen = source.String("data") == imageData && source.String("media_type") == "image/png"
				}
				if text := item.String("text"); strings.Contains(text, " saved to ") {
					paths = append(paths, strings.Split(text, " saved to ")[1])
				}
			}
		}
	}
	if !imageSeen || len(paths) != 2 {
		t.Fatal("generic MCP normalized image and saved binary instructions absent", imageSeen, paths)
	}
	for _, path := range paths {
		data, e := os.ReadFile(path)
		if e != nil || !strings.Contains(path, m.Controller.SessionID()) {
			t.Fatal("actual binary file/SID absent", path, e)
		}
		want := binaryData
		if strings.HasSuffix(path, ".wav") {
			want = []byte{0, 1, 63, 10}
		}
		if !bytes.Equal(data, want) {
			t.Fatal("physical rich result file mismatch", path, data)
		}
	}
	if !bytes.Contains(rawMain(requests[2]), binaryData[:len(binaryData)-1]) {
		t.Fatal("actual Read result missing from next model POST")
	}
	if ordinary {
		if approvals.Load() == 0 {
			t.Fatal("ordinary MCP ASK did not reach actual permission response")
		}
		var records []protocol.JSONObject
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error { records = m.Controller.History(); return nil }); e != nil {
			t.Fatal(e)
		}
		persistMCPRichPrivateReceipt(t, m, receiptRoot, paths, rawMain(records), "", counts["tools/call"], approvals.Load(), true, false)
	}
	if bytes.Contains(rawMain(requests[1]), []byte("AAE/Cg==")) || bytes.Contains(rawMain(requests[1]), []byte(base64.StdEncoding.EncodeToString(binaryData))) {
		t.Fatal("raw audio/resource base64 leaked into model request")
	}
}

func persistMCPRichPrivateReceipt(t *testing.T, m *MainPrint, root string, files []string, history []byte, key string, mcpCalls int, approvals int32, imageRequested, official bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := m.Host.WaitTranscriptRuntime(ctx); e != nil {
		t.Fatal(e)
	}
	if m.options.Trace == nil {
		t.Fatal("actual default model trace missing")
	}
	if e := m.options.Trace.Drain(ctx); e != nil {
		t.Fatal(e)
	}
	tracePath := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", "traces", m.Controller.SessionID()+".jsonl")
	trace, e := os.ReadFile(tracePath)
	if e != nil {
		t.Fatal(e)
	}
	transcriptPath := ""
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		directory, e := m.Controller.ToolResultsDirectoryOnOwner(scope)
		if e == nil {
			transcriptPath = filepath.Join(filepath.Dir(filepath.Dir(directory)), m.Controller.SessionID()+".jsonl")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	canonical, e := os.ReadFile(transcriptPath)
	if e != nil {
		t.Fatal(e)
	}
	if key != "" && (bytes.Contains(history, []byte(key)) || bytes.Contains(trace, []byte(key)) || bytes.Contains(canonical, []byte(key))) {
		t.Fatal("official credential persisted in canonical/trace")
	}
	if e = os.MkdirAll(filepath.Join(root, "media"), 0700); e != nil {
		t.Fatal(e)
	}
	rows := []any{}
	for _, path := range files {
		p, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		destination := filepath.Join(root, "media", filepath.Base(path))
		if e = os.WriteFile(destination, p, 0600); e != nil {
			t.Fatal(e)
		}
		rows = append(rows, map[string]any{"name": filepath.Base(path), "sha256": fmt.Sprintf("%x", sha256.Sum256(p)), "bytes": len(p)})
	}
	for name, p := range map[string][]byte{"history.json": history, "canonical.jsonl": canonical, "trace.jsonl": trace} {
		if e = os.WriteFile(filepath.Join(root, name), p, 0600); e != nil {
			t.Fatal(e)
		}
	}
	receipt := map[string]any{"sessionId": m.Controller.SessionID(), "official": official, "mcpCalls": mcpCalls, "permissionResponses": approvals, "imageRequested": imageRequested, "toolsDefault": m.options.Arguments.Tools == nil, "systemPromptDefault": m.options.Arguments.SystemPrompt == "", "permissionMode": m.options.Arguments.PermissionMode, "credentialAbsent": true, "files": rows, "historySha256": fmt.Sprintf("%x", sha256.Sum256(history)), "canonicalSha256": fmt.Sprintf("%x", sha256.Sum256(canonical)), "traceSha256": fmt.Sprintf("%x", sha256.Sum256(trace))}
	if e = os.WriteFile(filepath.Join(root, "receipt.json"), rawMain(receipt), 0600); e != nil {
		t.Fatal(e)
	}
}
