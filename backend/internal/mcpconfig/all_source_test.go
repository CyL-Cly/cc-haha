package mcpconfig

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

func TestAllClaudeAIConfigurationsActualSourceMergePolicyAndDisabledDedup(t *testing.T) {
	rows := []struct {
		manual, organization, settings string
		disabled                       []string
	}{
		{`{}`, `{"claude.ai A":{"type":"claudeai-proxy","url":"https://vendor.invalid/a","id":"id-a","scope":"claudeai"}}`, `{}`, nil},
		{`{"manual":{"type":"http","url":"https://vendor.invalid/a","scope":"user"}}`, `{"claude.ai A":{"type":"claudeai-proxy","url":"https://vendor.invalid/a","id":"id-a","scope":"claudeai"},"claude.ai B":{"type":"claudeai-proxy","url":"https://vendor.invalid/b","id":"id-b","scope":"claudeai"}}`, `{}`, nil},
		{`{"manual":{"type":"http","url":"https://vendor.invalid/a","scope":"user"}}`, `{"claude.ai A":{"type":"claudeai-proxy","url":"https://vendor.invalid/a","id":"id-a","scope":"claudeai"}}`, `{}`, []string{"manual"}},
		{`{"same":{"type":"http","url":"https://vendor.invalid/manual","scope":"user"}}`, `{"same":{"type":"claudeai-proxy","url":"https://vendor.invalid/organization","id":"other","scope":"claudeai"}}`, `{}`, nil},
		{`{}`, `{"claude.ai A":{"type":"claudeai-proxy","url":"https://vendor.invalid/a","id":"id-a","scope":"claudeai"}}`, `{"deniedMcpServers":[{"serverUrl":"https://vendor.invalid/a"}]}`, nil},
		{`{}`, `{"claude.ai A":{"type":"claudeai-proxy","url":"https://vendor.invalid/a","id":"id-a","scope":"claudeai"}}`, `{"allowedMcpServers":[]}`, nil},
	}
	for index, row := range rows {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			bun := os.Getenv("CC_HAHA_SOURCE_BUN")
			if bun == "" {
				t.Skip("Source Bun not configured")
			}
			s, home, settings, _, _, _, _ := fixture(t)
			*settings = parse(t, row.settings)
			manual, organization := parse(t, row.manual), parse(t, row.organization)
			s.options.Global = func(context.Context) (protocol.JSONObject, error) {
				return parse(t, `{"mcpServers":`+row.manual+`}`), nil
			}
			s.options.Enabled = func(_ context.Context, name string, _ protocol.JSONObject) (bool, error) {
				for _, disabled := range row.disabled {
					if disabled == name {
						return false, nil
					}
				}
				return true, nil
			}
			actual, e := s.AllConfigurations(context.Background(), organization)
			if e != nil {
				t.Fatal(e)
			}
			input, _ := json.Marshal(map[string]any{"home": home, "manual": manual, "organization": organization, "settings": *settings, "disabled": row.disabled})
			path, _ := filepath.Abs("all.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MCP_ALL_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, e := cmd.Output()
			if e != nil {
				t.Fatal(e, stderr.String())
			}
			var source struct {
				Result  json.RawMessage `json:"result"`
				Fetches int             `json:"fetches"`
			}
			if e = json.Unmarshal(out, &source); e != nil {
				t.Fatal(e, string(out))
			}
			if source.Fetches != 1 {
				t.Fatal("original fetch producer not invoked")
			}
			encoded, _ := json.Marshal(map[string]any{"servers": actual.Servers, "errors": actual.Errors})
			var expected, decoded any
			if json.Unmarshal(source.Result, &expected) != nil || json.Unmarshal(encoded, &decoded) != nil || !reflect.DeepEqual(expected, decoded) {
				t.Fatalf("actual Source %s Go %s", source.Result, encoded)
			}
		})
	}
}
