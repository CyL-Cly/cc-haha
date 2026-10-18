package application

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalAgentMCPActualSourceFirstDeclarationTransportsNamesAndScopeOrder(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source required")
	}
	profiles := []protocol.JSONObject{}
	names := []string{"ä-server", "ßplugin", "ǰplugin", "ﬃplugin", "αβ", "𐐨worker", "�work", "", "helloWorld", "école"}
	for _, transport := range []string{"stdio", "sse", "http", "ws", "sdk", "claudeai-proxy", "sse-ide", "ws-ide", ""} {
		config := map[string]any{"url": "http://127.0.0.1/owned", "command": "owned-command", "args": []string{"private"}}
		if transport != "" {
			config["type"] = transport
		}
		agents := array([]byte(`[{"agentType":"first","mcpServers":["global-reference",{"skip-multi":{"type":"stdio"},"other":{"type":"stdio"}}]},{"agentType":"first","mcpServers":[]},{"agentType":"second","mcpServers":[{"shared":{"type":"http","url":"must-not-win"}},{"ä":{"type":"stdio","command":"a"}},{"Z":{"type":"ws","url":"ws://127.0.0.1"}},{"\ud800":{"type":"stdio","command":"lone"}}]}]`))
		firstSpecs := []json.RawMessage{raw("global-reference"), raw(map[string]any{"shared": config})}
		agents[0].Set("mcpServers", raw(firstSpecs))
		agents[1].Set("mcpServers", raw([]any{map[string]any{"shared": map[string]string{"type": "sse", "url": "also-must-not-win"}}}))
		servers := []protocol.JSONObject{}
		for _, scope := range []string{"dynamic", "enterprise", "user", "local", "project", "unknown"} {
			for _, name := range []string{"Z", "ä", "a"} {
				servers = append(servers, obj(raw(map[string]any{"name": scope + name, "scope": scope, "config": map[string]string{"type": "stdio"}})))
			}
		}
		servers = append(servers, obj(raw(map[string]any{"name": "ide", "scope": "user", "config": map[string]string{"type": "stdio"}})), obj(raw(map[string]any{"name": "Proxy", "scope": "user", "config": map[string]string{"type": "claudeai-proxy"}})))
		profiles = append(profiles, obj(raw(map[string]any{"agents": agents, "servers": servers})))
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-mcp-agents.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MCP_AGENTS_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "profiles": profiles, "names": names}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual agent MCP Source %v %.6000s", err, output)
	}
	var source []struct {
		Agents      []protocol.JSONObject
		Order       []protocol.JSONObject
		Capitalized []string
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	for i, profile := range profiles {
		for j, name := range names {
			if actual := terminalMCPCapitalize(name); actual != source[i].Capitalized[j] {
				t.Fatalf("Source actual MCP server heading %q actual%q Source%q", name, actual, source[i].Capitalized[j])
			}
		}
		agents, err := mcpconfig.AgentServers(array(profile.Get("agents")))
		if err != nil {
			t.Fatal(err)
		}
		actual, _ := protocol.ParseAndStringifyJavaScriptJSON(raw(agents))
		want, _ := protocol.ParseAndStringifyJavaScriptJSON(raw(source[i].Agents))
		if string(actual) != string(want) {
			t.Fatalf("Source agent MCP %d actual%s Source%s", i, actual, want)
		}
		items, err := terminalMCPListItems(array(profile.Get("servers")), agents)
		if err != nil {
			t.Fatal(err)
		}
		order := []protocol.JSONObject{}
		for _, item := range items {
			order = append(order, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: item.Get("name")}, protocol.JSONField{Name: "agentOnly", Value: raw(string(item.Get("agentOnly")) == "true")}))
		}
		actual, _ = protocol.ParseAndStringifyJavaScriptJSON(raw(order))
		want, _ = protocol.ParseAndStringifyJavaScriptJSON(raw(source[i].Order))
		if string(actual) != string(want) {
			t.Fatalf("Source MCP scope order %d actual%s Source%s", i, actual, want)
		}
	}
}
