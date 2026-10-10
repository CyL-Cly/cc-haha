package mcpcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestMCPCLIParsingPrimitivesMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-mcp-primitives.fixture.ts")
	rows := []map[string]any{}
	for _, headers := range [][]string{{}, {"X-Z: last", "X-A:  first  ", "X-Z: replaced"}, {"X: v:with:colon"}, {"No colon"}, {" : empty name"}, {"\ufeff X \u00a0: \ufeff value\u00a0"}, {"__proto__: ignored", "constructor: kept"}, {"quoted\"header\n"}, {" : quoted\"value\n"}} {
		rows = append(rows, map[string]any{"kind": "headers", "values": headers})
	}
	for _, env := range [][]string{{}, {"A=1=2", "B=", "A=last"}, {"BAD"}, {"=no key"}, {" 空 = 保留 "}, {"__proto__=ignored", "constructor=kept"}} {
		rows = append(rows, map[string]any{"kind": "env", "values": env})
	}
	for _, value := range []string{"", "stdio", "sse", "http", "HTTPS", "ws", " stdio"} {
		rows = append(rows, map[string]any{"kind": "transport", "value": value})
	}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_MCP_PRIMITIVES_FIXTURE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected []json.RawMessage
	if err := json.Unmarshal(output, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		var value any
		var failure error
		switch row["kind"] {
		case "headers":
			value, failure = ParseHeaders(row["values"].([]string))
		case "env":
			value, failure = ParseEnvironment(row["values"].([]string))
		case "transport":
			value, failure = Transport(row["value"].(string))
		}
		actual := map[string]any{"value": value}
		if failure != nil {
			actual = map[string]any{"error": failure.Error()}
		}
		raw, _ := json.Marshal(actual)
		got, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
		want, _ := protocol.ParseAndStringifyJavaScriptJSON(expected[i])
		var a, b any
		_ = json.Unmarshal(got, &a)
		_ = json.Unmarshal(want, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("row%d Go%s Source%s", i, got, want)
		}
	}
}
