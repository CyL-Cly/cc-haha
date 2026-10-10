package mcpcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sourceServeHost struct{}

func (sourceServeHost) Catalog(context.Context) ([]protocol.JSONObject, error) { return nil, nil }
func (sourceServeHost) Call(_ context.Context, name string, _ json.RawMessage) (protocol.JSONObject, error) {
	return protocol.JSONObject{}, &ServeRPCError{-32603, "Tool " + name + " not found"}
}
func (sourceServeHost) Close(context.Context) error { return nil }

func TestMCPServeProtocolAgainstActualOriginalSDKServer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings.json", ".claude.json"} {
		_ = os.WriteFile(filepath.Join(home, ".claude", name), []byte(`{}`), 0600)
	}
	rows := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"private","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"private","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"private-future","capabilities":{},"clientInfo":{"name":"private","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":5,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":6,"method":"private/unknown","params":{}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"Absent","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"Absent","arguments":null}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"Absent","arguments":[]}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`,
		`{"jsonrpc":"private","id":11,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":12,"method":1}`,
	}
	var input bytes.Buffer
	for _, row := range rows {
		input.WriteString(row + "\n")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-mcp-serve.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_MCP_SERVE_SOURCE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ENABLE_TOOL_SEARCH=false", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input.Bytes())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	expected, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var actual bytes.Buffer
	if err = Serve(ctx, ServeOptions{Version: "fixture", Input: bytes.NewReader(input.Bytes()), Output: &actual, Host: sourceServeHost{}}); err != nil {
		t.Fatal(err)
	}
	decode := func(raw []byte) []map[string]any {
		out := []map[string]any{}
		for _, line := range bytes.Split(raw, []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			var value map[string]any
			if err = json.Unmarshal(line, &value); err != nil {
				t.Fatal(err, string(line))
			}
			if result, ok := value["result"].(map[string]any); ok {
				if info, ok := result["serverInfo"].(map[string]any); ok {
					info["version"] = "fixture"
				}
			}
			out = append(out, value)
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["id"].(float64) < out[j]["id"].(float64) })
		return out
	}
	x, y := decode(actual.Bytes()), decode(expected)
	if !reflect.DeepEqual(x, y) {
		a, _ := json.Marshal(x)
		b, _ := json.Marshal(y)
		t.Errorf("Go%s\nSource%s", a, b)
	}
}
