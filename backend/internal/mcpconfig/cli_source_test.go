package mcpconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActualMainCLIConfigurationInputsPolicyReservedEnterpriseAndErrors(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	s, home, settings, policy, _, _, _ := fixture(t)
	rows := []map[string]any{}
	add := func(inputs []string, strict bool, extra map[string]any) {
		row := map[string]any{"inputs": inputs, "strict": strict}
		for k, v := range extra {
			row[k] = v
		}
		rows = append(rows, row)
	}
	for _, raw := range []string{`{"mcpServers":{"peer":{"command":"private"}}}`, `{"mcpServers":{"peer":{"type":"http","url":"http://127.0.0.1:9","extra":true}}}`, `{"mcpServers":{"computer-use":{"type":"sdk","name":"native"}}}`, `{"mcpServers":{"computer-use":{"command":"private"}}}`, `{"mcpServers":{"claude-in-chrome":{"command":"private"}}}`, `{"mcpServers":{"claude-in-chrome":{"type":"sdk","name":"native"}}}`, `{"mcpServers":{"computer/use":{"command":"private"}}}`, `{"mcpServers":{"bad":{"command":3}}}`, `[]`, `null`, `false`, `{bad`} {
		add([]string{raw}, false, nil)
	}
	add([]string{"", " \ufeff ", `{"mcpServers":{}}`}, true, nil)
	add([]string{`{"mcpServers":{"peer":{"command":"first"}}}`, "config.json", `{"mcpServers":{"peer":{"command":"final"},"next":{"type":"sdk","name":"provided-name"}}}`}, false, map[string]any{"files": map[string]string{"config.json": `{"mcpServers":{"peer":{"command":"file"},"file":{"command":"other"}}}`}})
	add([]string{"missing.json", `{"mcpServers":{"bad":{"command":false}}}`}, false, nil)
	for _, server := range []string{`{"regular":{"command":"private"}}`, `{"sdk":{"type":"sdk","name":"other"}}`, `{"sdk":{"type":"sdk","name":"claude-vscode"}}`, `{}`} {
		for _, strict := range []bool{false, true} {
			add([]string{`{"mcpServers":` + server + `}`}, strict, map[string]any{"enterprise": map[string]any{"mcpServers": map[string]any{}}})
		}
	}
	for _, strict := range []bool{false, true} {
		add([]string{`{"mcpServers":{"peer":{"command":"private"},"second":{"command":"private2"},"sdk":{"type":"sdk","name":"provided"}}}`}, strict, map[string]any{"settings": map[string]any{"allowedMcpServers": []any{}}})
	}
	fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-mcp-input.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_MCP_INPUT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected []struct {
		Servers  json.RawMessage
		Warnings []string
		Error    *string
	}
	if err = json.Unmarshal(out, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(out))
	}
	for i, row := range rows {
		s.enterpriseExists = nil
		*settings = protocol.NewJSONObject()
		*policy = protocol.NewJSONObject()
		if value := row["settings"]; value != nil {
			raw, _ := json.Marshal(value)
			_ = settings.UnmarshalJSON(raw)
		}
		if value := row["policy"]; value != nil {
			raw, _ := json.Marshal(value)
			_ = policy.UnmarshalJSON(raw)
		}
		enterprise := filepath.Join(s.options.ManagedRoot, "managed-mcp.json")
		_ = os.Remove(enterprise)
		if value := row["enterprise"]; value != nil {
			_ = os.MkdirAll(filepath.Dir(enterprise), 0700)
			raw, _ := json.Marshal(value)
			_ = os.WriteFile(enterprise, raw, 0600)
		}
		if files, ok := row["files"].(map[string]string); ok {
			for path, text := range files {
				_ = os.WriteFile(filepath.Join(home, path), []byte(text), 0600)
			}
		}
		servers, warnings, err := s.CLI(ctx, row["inputs"].([]string), row["strict"].(bool))
		if len(warnings) == 0 {
			warnings = []string{}
		}
		if !reflect.DeepEqual(warnings, expected[i].Warnings) {
			t.Error(i, "warnings", warnings, expected[i].Warnings)
		}
		if expected[i].Error != nil {
			if err == nil || strings.TrimSuffix(err.Error(), "\n") != strings.TrimSuffix(*expected[i].Error, "\n") {
				t.Error(i, "error", err, *expected[i].Error)
			}
			continue
		}
		if err != nil {
			t.Error(i, err)
			continue
		}
		raw, _ := json.Marshal(servers)
		var left, right any
		_ = json.Unmarshal(raw, &left)
		_ = json.Unmarshal(expected[i].Servers, &right)
		if !reflect.DeepEqual(left, right) {
			t.Error(i, "servers", string(raw), string(expected[i].Servers))
		}
	}
}
