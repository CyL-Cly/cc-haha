package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestMCPConfigurationsUseActualSharedSourcesPluginLoaderAndPolicy(t *testing.T) {
	for _, mode := range []string{"merged", "enterprise", "restricted", "disabled-duplicate"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativePluginFixture(t)
			files := map[string]string{}
			write := func(path, content string) { files[path] = content; f.write(t, path, content) }
			settings := `{"enabledPlugins":{"sample@mkt":true},"enableAllProjectMcpServers":true,"deniedMcpServers":[{"serverName":"blocked"}]}`
			write(filepath.Join(f.config, "settings.json"), settings)
			write(filepath.Join(f.plugin, ".claude-plugin/plugin.json"), `{"name":"sample","mcpServers":{"manual-copy":{"command":"node","args":["user"]},"plugin":{"type":"http","url":"https://plugin.invalid/api"},"blocked":{"type":"sse","url":"https://blocked.invalid"}}}`)
			write(filepath.Join(f.home, ".mcp.json"), `{"mcpServers":{"parent":{"command":"parent"},"shared":{"command":"parent-shared"}}}`)
			write(filepath.Join(f.cwd, ".mcp.json"), `{"mcpServers":{"project":{"command":"project"},"shared":{"command":"project-shared"},"blocked":{"command":"blocked"}}}`)
			canonical, err := filepath.EvalSymlinks(f.cwd)
			if err != nil {
				t.Fatal(err)
			}
			project := map[string]any{"mcpServers": map[string]any{"local": map[string]any{"command": "local"}, "shared": map[string]any{"command": "local-shared"}}}
			if mode == "disabled-duplicate" {
				project["disabledMcpServers"] = []string{"user"}
			}
			global, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"user": map[string]any{"command": "node", "args": []string{"user"}}, "shared": map[string]any{"command": "user-shared"}}, "projects": map[string]any{canonical: project}})
			write(filepath.Join(f.config, ".claude.json"), string(global))
			policy := "{}"
			if mode == "restricted" {
				policy = `{"strictPluginOnlyCustomization":["mcp"]}`
			}
			if mode == "enterprise" {
				write(filepath.Join(f.managed, "managed-mcp.json"), `{"mcpServers":{"only":{"type":"http","url":"https://enterprise.invalid"},"blocked":{"command":"blocked"}}}`)
				policy = `{"deniedMcpServers":[{"serverName":"blocked"}]}`
			}
			write(filepath.Join(f.managed, "managed-settings.json"), policy)
			// Include the real marketplace files, rather than supplying preloaded
			// plugin objects to either implementation.
			for _, path := range []string{filepath.Join(f.config, "plugins/known_marketplaces.json"), filepath.Join(f.market, ".claude-plugin/marketplace.json"), filepath.Join(f.cwd, ".claude/settings.json")} {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				files[path] = string(data)
			}
			p, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close(context.Background())
			secure, err := securestorage.New(securestorage.Options{Home: f.home, ConfigDir: f.config, Platform: "linux", Environment: f.n.Environment})
			if err != nil {
				t.Fatal(err)
			}
			options, err := p.PluginOptions(secure)
			if err != nil {
				t.Fatal(err)
			}
			plugins, _, err := p.PluginMCP(options, secure)
			if err != nil {
				t.Fatal(err)
			}
			configs, err := p.MCPConfigurations(plugins, true)
			if err != nil {
				t.Fatal(err)
			}
			observations := []any{}
			cases := []map[string]any{}
			for _, scope := range []string{"user", "project", "local", "enterprise"} {
				value, err := configs.Scope(context.Background(), scope)
				if err != nil {
					t.Fatal(err)
				}
				observations = append(observations, value)
				cases = append(cases, map[string]any{"action": "scope", "scope": scope, "settings": json.RawMessage(settings), "policy": json.RawMessage(policy), "nonInteractive": true})
			}
			value, err := configs.Configurations(context.Background(), protocol.NewJSONObject(), protocol.NewJSONObject())
			if err != nil {
				t.Fatal(err)
			}
			observations = append(observations, value)
			cases = append(cases, map[string]any{"action": "configurations", "settings": json.RawMessage(settings), "policy": json.RawMessage(policy), "nonInteractive": true})
			for _, name := range []string{"shared", "user", "project", "parent", "local", "only", "blocked", "plugin:sample:plugin", "missing"} {
				config, err := configs.ByName(context.Background(), name)
				if err != nil {
					t.Fatal(err)
				}
				observations = append(observations, config)
				cases = append(cases, map[string]any{"action": "byName", "name": name, "settings": json.RawMessage(settings), "policy": json.RawMessage(policy), "nonInteractive": true})
			}
			if mode == "merged" && objectForMCP(value.Servers.Get("shared")).String("command") != "local-shared" {
				t.Fatal("scope precedence", value)
			}
			if mode == "restricted" && len(value.Servers.OwnProperties()) != 3 {
				t.Fatal("plugin-only policy", value)
			}
			if mode == "disabled-duplicate" && value.Servers.Get("plugin:sample:manual-copy") == nil {
				t.Fatal("disabled manual server suppressed plugin", value)
			}
			bun := os.Getenv("CC_HAHA_SOURCE_BUN")
			if bun == "" {
				t.Skip("actual Source configuration comparison requires pinned Bun")
			}
			fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-mcp-config.fixture.ts")
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bun, "run", fixture)
			cmd.Dir = f.home
			cmd.Env = []string{"PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_MCP_CONFIG_FIXTURE=1"}
			input, _ := json.Marshal(map[string]any{"cases": cases, "runtime": map[string]any{"cwd": f.n.CWD, "files": files}})
			cmd.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			actual, err := cmd.Output()
			if err != nil {
				t.Fatalf("Source actual config %v\n%s", err, stderr.String())
			}
			expected, _ := json.Marshal(observations)
			var a, b []any
			if json.Unmarshal(actual, &a) != nil || json.Unmarshal(expected, &b) != nil {
				t.Fatalf("invalid Source output %s", actual)
			}
			if !reflect.DeepEqual(a, b) {
				for i := range a {
					if !reflect.DeepEqual(a[i], b[i]) {
						x, _ := json.Marshal(a[i])
						y, _ := json.Marshal(b[i])
						t.Fatalf("case %d\nSource %s\nGo %s", i, x, y)
					}
				}
				t.Fatal("scope observation mismatch")
			}
		})
	}
}
func objectForMCP(raw json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(raw)
	return o
}
