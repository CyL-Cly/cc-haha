package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func validationCorpus() []map[string]any {
	cases := []map[string]any{}
	add := func(method, text string) {
		var v any
		_ = json.Unmarshal([]byte(text), &v)
		cases = append(cases, map[string]any{"method": method, "result": v})
	}
	for _, method := range []string{"tools/list", "resources/list", "resources/templates/list", "resources/read", "prompts/list", "prompts/get", "tools/call", "ping"} {
		result := fixtureResult(rpcMessage{Method: method}).Result
		add(method, string(result))
	}
	for _, s := range []string{
		`{"protocolVersion":"2025-11-25","serverInfo":{"name":"x","version":"1","unknown":"strip","icons":[{"src":"x","unknown":"strip"}]},"capabilities":{"tools":{"listChanged":true,"unknown":"strip"},"unknown":"strip","experimental":{"arbitrary":[]},"tasks":{"list":{},"requests":{"tools":{"call":[],"unknown":"keep"},"unknown":"keep"},"unknown":"keep"}},"unknown":"keep"}`,
		`{"protocolVersion":"2025-11-25","serverInfo":{"name":"x","version":"1"},"capabilities":{"resources":{"subscribe":"true"}}}`,
		`{"protocolVersion":"2025-11-25","serverInfo":{"name":"x","version":"1"},"capabilities":{"experimental":{"flag":true}}}`,
		`{"protocolVersion":"2025-11-25","serverInfo":{"name":"x","version":"1"},"capabilities":{"tasks":{"requests":{"tools":{"call":false}}}}}`,
	} {
		add("initialize", s)
	}
	for _, s := range []string{
		`{"tools":[]}`, `{"tools":null}`, `{"tools":[{"name":1,"inputSchema":{"type":"object"}}]}`,
		`{"tools":[{"name":"tool","inputSchema":{"type":"array"}}]}`,
		`{"tools":[{"name":"tool","inputSchema":{"type":"object","properties":{"x":false}}}]}`,
		`{"tools":[{"name":"tool","inputSchema":{"type":"object","properties":{"x":[]}}}]}`,
		`{"tools":[{"name":"tool","inputSchema":{"type":"object","required":[1]}}]}`,
		`{"tools":[{"name":"tool","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":"true"}}]}`,
		`{"tools":[{"name":"tool","inputSchema":{"type":"object"},"execution":{"taskSupport":"required","unknown":true}}]}`,
		`{"tools":[{"name":"tool","inputSchema":{"type":"object"},"execution":{"taskSupport":"invalid"}}]}`,
		`{"tools":[{"name":"tool","inputSchema":{"type":"object"},"icons":[{"src":"data:image/png;base64,","sizes":["48x48"],"theme":"dark","unknown":"strip"}]}]}`,
		`{"tools":[{"name":"tool","inputSchema":{"type":"object"},"icons":[{"src":"x","theme":"invalid"}]}]}`,
		`{"tools":[{"name":"tool","inputSchema":{"type":"object"},"icons":[{"src":"x","sizes":[1]}]}]}`,
		`{"tools":[],"_meta":{"progressToken":"2","io.modelcontextprotocol/related-task":{"taskId":"t","unknown":"strip"},"unknown":"keep"}}`,
		`{"tools":[],"_meta":{"progressToken":false}}`,
		`{"tools":[],"nextCursor":4}`,
	} {
		add("tools/list", s)
	}
	for _, s := range []string{
		`{}`, `{"structuredContent":{"ok":true}}`, `{"content":null}`, `{"content":[],"isError":"true"}`,
		`{"content":[{"type":"image","mimeType":"image/png","data":"YQ"}]}`,
		`{"content":[{"type":"audio","mimeType":"audio/wav","data":"Y Q==\n"}]}`,
		`{"content":[{"type":"image","mimeType":"image/png","data":"%%%"}]}`,
		`{"content":[{"type":"text","text":"x","annotations":{"audience":["user"],"priority":0.5,"lastModified":"2026-10-06T00:00:00+08:00","unknown":"strip"}}]}`,
		`{"content":[{"type":"text","text":"x","annotations":{"audience":["unknown"]}}]}`,
		`{"content":[{"type":"text","text":"x","annotations":{"priority":2}}]}`,
		`{"content":[{"type":"text","text":"x","annotations":{"lastModified":"today"}}]}`,
		`{"content":[{"type":"resource_link","name":"x","uri":"file:///x","size":5,"unknown":"strip"}]}`,
		`{"content":[{"type":"resource","resource":{"uri":"file:///x","text":false,"blob":"YQ==","unknown":"strip"}}]}`,
		`{"content":[{"type":"resource","resource":{"uri":"file:///x","text":"good","blob":"invalid","unknown":"strip"}}]}`,
		`{"content":[{"type":"resource","resource":{"uri":"file:///x","blob":"invalid"}}]}`,
		`{"content":[{"type":"video","data":""}]}`,
	} {
		add("tools/call", s)
	}
	for _, s := range []string{`{"prompts":[{"name":"review","arguments":[{"name":"x","required":false}]}]}`, `{"prompts":[{"name":"review","arguments":[{"required":true}]}]}`, `{"prompts":[{"name":"review","arguments":[{"name":"x","required":"yes"}]}]}`} {
		add("prompts/list", s)
	}
	for _, s := range []string{`{"messages":[{"role":"system","content":{"type":"text","text":"x"}}]}`, `{"messages":[{"role":"assistant","content":{"type":"image","mimeType":"image/png","data":""}}]}`} {
		add("prompts/get", s)
	}
	for _, s := range []string{`{}`, `{"unexpected":true}`, `{"_meta":{"owned":"yes"}}`} {
		add("ping", s)
	}
	return cases
}

func TestSchemaAndMessageAcceptanceMatchActualSDK(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	cases := validationCorpus()
	messages := []json.RawMessage{
		[]byte(`{"jsonrpc":"2.0","id":0,"result":{}}`), []byte(`{"jsonrpc":"2.0","id":"0","result":{}}`),
		[]byte(`{"jsonrpc":"2.0","id":null,"result":{}}`), []byte(`{"jsonrpc":"2.0","id":1.5,"result":{}}`),
		[]byte(`{"jsonrpc":"2.0","id":0.0,"result":{}}`), []byte(`{"jsonrpc":"2.0","id":9007199254740992,"result":{}}`),
		[]byte(`{"jsonrpc":"2.0","method":"","id":0}`), []byte(`{"jsonrpc":"2.0","method":"notifications/changed"}`),
		[]byte(`{"jsonrpc":"2.0","method":"x","id":0,"params":null}`), []byte(`{"jsonrpc":"2.0","method":"x","id":0,"unknown":1}`),
		[]byte(`{"jsonrpc":"2.0","id":0,"result":[]}`), []byte(`{"jsonrpc":"2.0","id":0,"error":{"code":-32602.0,"message":"bad"}}`),
		[]byte(`{"jsonrpc":"2.0","id":0,"error":{"code":"-32602","message":"bad"}}`), []byte(`{"jsonrpc":"2.0","id":0,"result":{},"error":{"code":1,"message":"bad"}}`),
		[]byte(`{"jsonrpc":"2.0","error":{"code":-32600,"message":"invalid request"}}`),
	}
	results := []any{}
	accepted := []bool{}
	escapeCases := []string{"", "name with spaces", "C:\\Program Files\\server.cmd", "quoted\"name", "trailing\\", "before\\\"after", "&|^%![]();,`<>*?", `node_modules\.bin\owned.cmd`, "🧠 中文"}
	escapes := []any{}
	for _, v := range escapeCases {
		escapes = append(escapes, map[string]string{"command": escapeWindowsMeta(v), "argument": escapeWindowsArgument(v, false), "double": escapeWindowsArgument(v, true)})
	}
	for _, item := range cases {
		result, err := validateResult(item["method"].(string), nested(raw(item["result"])))
		if err != nil {
			results = append(results, map[string]any{"ok": false})
		} else {
			results = append(results, map[string]any{"ok": true, "result": result, "resultJSON": string(raw(result))})
		}
	}
	for _, item := range messages {
		_, err := decodeMessage(item)
		accepted = append(accepted, err == nil)
	}
	home := t.TempDir()
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-mcp-client.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_MCP_CLIENT_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"mode": "validation", "cases": cases, "messages": messages, "windowsEscapes": escapeCases}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	expected, err := cmd.Output()
	if err != nil {
		t.Fatalf("SDK fixture: %v %s", err, bounded(stderr.String()))
	}
	assertJSONEqual(t, raw(map[string]any{"results": results, "messages": accepted, "windowsEscapes": escapes}), expected)
}

func TestUTF16DescriptionAndNames(t *testing.T) {
	if got := string(TruncateDescription([]byte(`"a🧠b"`), 2)); got != `"a\ud83e… [truncated]"` {
		t.Fatalf("UTF16 slicing: %s", got)
	}
	if got := ToolName("🧠", "read.file"); got != "mcp______read_file" {
		t.Fatalf("name %s", got)
	}
	if got := string(TruncateDescription([]byte(`"short"`), 2048)); got != `"short"` {
		t.Fatal(got)
	}
}
