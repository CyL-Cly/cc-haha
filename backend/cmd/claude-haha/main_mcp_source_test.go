package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPCLIHandlersMatchActualSourceOutputAndMutationArguments(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	type scenario struct {
		args   []string
		source map[string]any
		setup  []string
	}
	rows := []scenario{
		{args: []string{"mcp", "add", "ordinary", "/usr/bin/true", "one", "two"}, source: map[string]any{"action": "add", "args": []string{"ordinary", "/usr/bin/true", "one", "two"}}},
		{args: []string{"mcp", "add", "ordinary", "/usr/bin/true", "-s", "user", "-e", "A=1=2", "-e", "B="}, source: map[string]any{"action": "add", "args": []string{"ordinary", "/usr/bin/true", "-s", "user", "-e", "A=1=2", "-e", "B="}}},
		{args: []string{"mcp", "add", "looks", "https://private.invalid/mcp"}, source: map[string]any{"action": "add", "args": []string{"looks", "https://private.invalid/mcp"}}},
		{args: []string{"mcp", "add", "--client-id", "ignored", "ordinary", "/usr/bin/true"}, source: map[string]any{"action": "add", "args": []string{"--client-id", "ignored", "ordinary", "/usr/bin/true"}}},
		{args: []string{"mcp", "add", "--transport", "http", "-H", "X-Z: a:b", "-H", "X-A: first", "-H", "X-Z: replace", "--callback-port", "234x", "remote", "http://127.0.0.1:1/mcp"}, source: map[string]any{"action": "add", "args": []string{"--transport", "http", "-H", "X-Z: a:b", "-H", "X-A: first", "-H", "X-Z: replace", "--callback-port", "234x", "remote", "http://127.0.0.1:1/mcp"}}},
		{args: []string{"mcp", "add", "--transport", "sse", "--client-id", "private-client", "--client-secret", "remote", "http://127.0.0.1:1/sse"}, source: map[string]any{"action": "add", "args": []string{"--transport", "sse", "--client-id", "private-client", "--client-secret", "remote", "http://127.0.0.1:1/sse"}, "secret": "fake-private-source-secret"}},
		{args: []string{"mcp", "add", "-s", "wrong", "ordinary", "/usr/bin/true"}, source: map[string]any{"action": "add", "args": []string{"-s", "wrong", "ordinary", "/usr/bin/true"}}},
		{args: []string{"mcp", "add", "-t", "ws", "ordinary", "/usr/bin/true"}, source: map[string]any{"action": "add", "args": []string{"-t", "ws", "ordinary", "/usr/bin/true"}}},
		{args: []string{"mcp", "add", "ordinary", "/usr/bin/true", "-e", "BAD"}, source: map[string]any{"action": "add", "args": []string{"ordinary", "/usr/bin/true", "-e", "BAD"}}},
		{args: []string{"mcp", "add", "-t", "http", "ordinary", "http://127.0.0.1:1", "-H", "BAD"}, source: map[string]any{"action": "add", "args": []string{"-t", "http", "ordinary", "http://127.0.0.1:1", "-H", "BAD"}}},
		{args: []string{"mcp", "add-json", "json", "{\"command\":\"/usr/bin/true\",\"args\":[\"arg\"]}"}, source: map[string]any{"action": "add-json", "name": "json", "json": "{\"command\":\"/usr/bin/true\",\"args\":[\"arg\"]}"}},
		{args: []string{"mcp", "remove", "existing", "-s", "user"}, setup: []string{"mcp", "add", "existing", "/usr/bin/true", "-s", "user"}, source: map[string]any{"action": "remove", "name": "existing", "options": map[string]any{"scope": "user"}, "configs": map[string]any{"existing": map[string]any{"type": "stdio", "command": "/usr/bin/true", "args": []string{}, "scope": "user"}}}},
		{args: []string{"mcp", "remove", "existing"}, setup: []string{"mcp", "add", "existing", "/usr/bin/true", "-s", "user"}, source: map[string]any{"action": "remove", "name": "existing", "global": map[string]any{"mcpServers": map[string]any{"existing": map[string]any{"command": "/usr/bin/true"}}}, "configs": map[string]any{"existing": map[string]any{"type": "stdio", "command": "/usr/bin/true", "args": []string{}, "scope": "user"}}}},
		{args: []string{"mcp", "remove", "missing"}, source: map[string]any{"action": "remove", "name": "missing"}},
		{args: []string{"mcp", "get", "missing"}, source: map[string]any{"action": "get", "name": "missing"}},
		{args: []string{"mcp", "list"}, source: map[string]any{"action": "list"}},
		{args: []string{"mcp", "reset-project-choices"}, source: map[string]any{"action": "reset"}},
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-mcp-handlers.fixture.ts")
	for i, row := range rows {
		t.Run(strings.Join(row.args, " "), func(t *testing.T) {
			n, root := mcpCLIPrivateNative(t)
			if row.setup != nil {
				code, _, _, err := mcpCLIExecute(t, n, row.setup)
				if code != 0 || err != nil {
					t.Fatal(code, err)
				}
			}
			n.Environment["MCP_CLIENT_SECRET"] = "fake-private-source-secret"
			code, out, diagnostics, failure := mcpCLIExecute(t, n, row.args)
			if failure != nil {
				diagnostics += failure.Error() + "\n"
			}
			row.source["locations"] = map[string]string{"local": filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json") + " [project: " + n.CWD + "]", "user": filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), "project": filepath.Join(n.CWD, ".mcp.json")}
			sourceHome := filepath.Join(root, "source-home")
			if err := os.MkdirAll(sourceHome, 0700); err != nil {
				t.Fatal(err)
			}
			input, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "rows": []any{row.source}})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = sourceHome
			cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(root), "CC_HAHA_CLI_MCP_HANDLERS_FIXTURE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			result, err := cmd.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var expected struct {
				Rows []struct {
					Code           int
					Stdout, Stderr string
					Effects        []map[string]any
				}
			}
			if err = json.Unmarshal(result, &expected); err != nil || len(expected.Rows) != 1 {
				t.Fatal(err, string(result))
			}
			want := expected.Rows[0]
			if code != want.Code || out != want.Stdout || diagnostics != want.Stderr {
				t.Errorf("row%d native(%d,%q,%q) Source(%d,%q,%q)", i, code, out, diagnostics, want.Code, want.Stdout, want.Stderr)
			}
			// Actual Source handlers expose the exact mutation arguments. The Go
			// writer is the shared production editor, so verify those persisted
			// fields at the actual path rather than replacing its mutation.
			for _, effect := range want.Effects {
				if effect["op"] != "add" {
					continue
				}
				name := effect["name"].(string)
				scope := effect["scope"].(string)
				data, err := os.ReadFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"))
				if err != nil {
					t.Fatal(err)
				}
				var global map[string]any
				if err = json.Unmarshal(data, &global); err != nil {
					t.Fatal(err)
				}
				container := global
				if scope == "local" {
					projects := global["projects"].(map[string]any)
					container = projects[n.CWD].(map[string]any)
				}
				servers := container["mcpServers"].(map[string]any)
				got, _ := json.Marshal(servers[name])
				wantConfig, _ := json.Marshal(effect["config"])
				if !bytes.Equal(got, wantConfig) {
					t.Errorf("persisted config native%s Source mutation%s", got, wantConfig)
				}
			}
		})
	}
}
