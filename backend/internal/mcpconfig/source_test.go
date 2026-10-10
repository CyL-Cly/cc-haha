package mcpconfig

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
)

func parse(t *testing.T, data string) protocol.JSONObject {
	t.Helper()
	var o protocol.JSONObject
	if err := o.UnmarshalJSON([]byte(data)); err != nil {
		t.Fatal(err)
	}
	return o
}
func fixture(t *testing.T) (*Service, string, *protocol.JSONObject, *protocol.JSONObject, map[string]string, *bool, *[]string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings, policy := protocol.NewJSONObject(), protocol.NewJSONObject()
	env := map[string]string{}
	interactive := false
	sources := []string{"userSettings", "projectSettings", "localSettings", "policySettings"}
	read := func(v *protocol.JSONObject) func(context.Context) (protocol.JSONObject, error) {
		return func(context.Context) (protocol.JSONObject, error) { return v.Clone(), nil }
	}
	s, err := New(Options{CWD: home, Platform: runtime.GOOS, ManagedRoot: filepath.Join(home, "managed"), Environment: func(k string) (string, bool) { v, ok := env[k]; return v, ok }, ReadFile: os.ReadFile, Settings: read(&settings), Policy: read(&policy), Global: func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, Project: func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, SourceEnabled: func(k string) bool {
		for _, v := range sources {
			if v == k {
				return true
			}
		}
		return false
	}, Plugins: func(context.Context) (Result, error) { return empty(), nil }, Enabled: func(context.Context, string, protocol.JSONObject) (bool, error) { return true, nil }, BypassConfirmed: func(context.Context) (bool, error) {
		return string(settings.Get("skipDangerousModePermissionPrompt")) == "true", nil
	}, NonInteractive: interactive})
	if err != nil {
		t.Fatal(err)
	}
	return s, home, &settings, &policy, env, &interactive, &sources
}
func TestMCPConfigurationProducerAgainstActualOriginalSource(t *testing.T) {
	s, home, settings, policy, env, nonInteractive, sources := fixture(t)
	cases := []map[string]any{}
	observations := []any{}
	add := func(row map[string]any) {
		if value, ok := row["settings"].(protocol.JSONObject); ok {
			*settings = value
		} else {
			*settings = protocol.NewJSONObject()
		}
		if value, ok := row["policy"].(protocol.JSONObject); ok {
			*policy = value
		} else {
			*policy = protocol.NewJSONObject()
		}
		for k := range env {
			delete(env, k)
		}
		if values, ok := row["env"].(map[string]string); ok {
			for k, v := range values {
				env[k] = v
			}
		}
		*sources = []string{"userSettings", "projectSettings", "localSettings", "policySettings"}
		if values, ok := row["sources"].([]string); ok {
			*sources = values
		}
		*nonInteractive, _ = row["nonInteractive"].(bool)
		s.options.NonInteractive = *nonInteractive
		var value any
		switch row["action"] {
		case "parse":
			data := row["raw"].(json.RawMessage)
			expand, _ := row["expand"].(bool)
			file, _ := row["file"].(string)
			config, failures := s.Parse(data, expand, row["scope"].(string), file)
			value = map[string]any{"config": config, "errors": failures}
		case "file":
			file := row["file"].(string)
			if data, ok := row["data"].(string); ok {
				if err := os.WriteFile(file, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			expand, _ := row["expand"].(bool)
			config, failures := s.ParseFile(file, expand, row["scope"].(string))
			value = map[string]any{"config": config, "errors": failures}
		case "signature":
			signature := Signature(row["config"].(protocol.JSONObject))
			if signature != "" {
				value = signature
			}
		case "hash":
			value = Hash(row["config"].(protocol.JSONObject))
		case "unwrap":
			value = UnwrapProxyURL(row["url"].(string))
		case "dedup":
			servers, suppressed := Dedup(row["servers"].(protocol.JSONObject), row["manual"].(protocol.JSONObject), row["plugins"].(bool))
			value = map[string]any{"servers": servers, "suppressed": suppressed}
		case "policy":
			allowed := protocol.NewJSONObject()
			blocked := []string{}
			for _, p := range row["servers"].(protocol.JSONObject).OwnProperties() {
				config := object(p.Value)
				if config.String("type") == "sdk" || Allowed(name(p), config, *settings, *policy) {
					_ = allowed.SetPropertyJSON(p.NameJSON, p.Value)
				} else {
					blocked = append(blocked, name(p))
				}
			}
			value = map[string]any{"allowed": allowed, "blocked": blocked}
		case "approval":
			status, err := s.ProjectStatus(context.Background(), row["name"].(string))
			if err != nil {
				t.Fatal(err)
			}
			value = status
		}
		cases = append(cases, row)
		observations = append(observations, value)
	}
	for _, raw := range []string{`null`, `[]`, `{}`, `{"mcpServers":null}`, `{"mcpServers":[]}`, `{"mcpServers":{"bad":null}}`, `{"mcpServers":{"bad":{"command":""}}}`, `{"mcpServers":{"bad":{"command":3,"args":[true]}}}`, `{"mcpServers":{"a":{"type":"http","url":"x","oauth":{"callbackPort":0,"authServerMetadataUrl":"invalid"}},"b":{"type":"bad"}}}`, `{"mcpServers":{"a":{"type":"http","url":"x","oauth":{"callbackPort":1.5}}}}`, `{"mcpServers":{"a":{"type":"stdio","command":"cmd","args":["x"],"env":{"1":"x","__proto__":"drop"},"extra":true},"b":{"type":"http","url":"invalid-url","headers":{"X":"Y"},"headersHelper":"cmd","oauth":{"clientId":"id","callbackPort":4,"authServerMetadataUrl":"https://example.invalid","xaa":true,"extra":true}},"c":{"type":"sse-ide","url":"x","ideName":"IDE","ideRunningInWindows":true},"d":{"type":"ws-ide","url":"x","ideName":"IDE","authToken":"fake"},"e":{"type":"sdk","name":"in-process"},"f":{"type":"claudeai-proxy","url":"x","id":"id"},"g":{"type":"ws","url":"x","oauth":false},"__proto__":{"command":"not-own"}},"extra":true}`} {
		add(map[string]any{"action": "parse", "raw": json.RawMessage(raw), "expand": false, "scope": "user"})
	}
	add(map[string]any{"action": "parse", "raw": json.RawMessage(`{"mcpServers":{"local":{"command":"${COMMAND}","args":["${MISSING}","${MISSING}","${FALLBACK:-one:-two}","${EMPTY:-default}"],"env":{"X":"${OTHER}","Y":"literal"}},"http":{"type":"http","url":"${URL}/api","headers":{"X":"${MISSING}"},"headersHelper":"${NOT_EXPANDED}"},"ide":{"type":"ws-ide","url":"${IDE}","ideName":"${NAME}"}}}`), "expand": true, "scope": "project", "file": filepath.Join(home, ".mcp.json"), "env": map[string]string{"COMMAND": "node", "EMPTY": "", "URL": "https://example.invalid"}})
	for i, data := range []any{nil, "", "\ufeff{}", "null", "false", "[]", `{"mcpServers":{"x":{"command":"node"}}}`, `{"mcpServers":{"x":{"type":"http","url":null}}}`} {
		add(map[string]any{"action": "file", "file": filepath.Join(home, "file-"+string(rune('a'+i))), "data": data, "expand": true, "scope": "enterprise"})
	}
	for _, raw := range []string{`{"command":"cmd","args":["x"],"env":{"different":"ignored"}}`, `{"type":"stdio","command":"cmd"}`, `{"type":"sdk","name":"x"}`, `{"type":"http","url":""}`, `{"type":"sse","url":"https://example.invalid/v2/ccr-sessions/id?mcp_url=https%3A%2F%2Fvendor.invalid%2F"}`} {
		add(map[string]any{"action": "signature", "config": parse(t, raw)})
	}
	for _, url := range []string{"not-url/v2/ccr-sessions/x", "https://example.invalid/v2/session_ingress/shttp/mcp/id?mcp_url=a+b", "https://example.invalid/plain?mcp_url=ignored", "https://example.invalid/v2/ccr-sessions/x?mcp_url="} {
		add(map[string]any{"action": "unwrap", "url": url})
	}
	for _, raw := range []string{`{"command":"cmd","args":["one","two"],"env":{"Z":"z","A":"a"},"scope":"user"}`, `{"scope":"project","env":{"A":"a","Z":"z"},"args":["one","two"],"command":"cmd"}`, `{"scope":"user","env":{"2":"two","1":"one","\ud800":"unpaired","😀":"pair","__proto__":"drop"},"oauth":{"scope":"preserved","nested":[{"b":2,"a":1}]},"url":"url"}`} {
		add(map[string]any{"action": "hash", "config": parse(t, raw)})
	}
	servers := parse(t, `{"one":{"command":"node","args":["x"],"scope":"plugin"},"two":{"command":"node","args":["x"],"env":{"different":"same-peer"}},"remote":{"type":"http","url":"https://api.example.invalid/v1"},"sdk":{"type":"sdk","name":"peer"}}`)
	manual := parse(t, `{"owned":{"type":"http","url":"https://api.example.invalid/v1"}}`)
	for _, plugins := range []bool{true, false} {
		add(map[string]any{"action": "dedup", "plugins": plugins, "servers": servers, "manual": manual})
	}
	for _, cfg := range []string{`{}`, `{"allowedMcpServers":[]}`, `{"allowedMcpServers":[{"serverName":"one"},{"serverName":"remote"}]}`, `{"allowedMcpServers":[{"serverName":"one"},{"serverCommand":["other"]},{"serverUrl":"https://*.example.invalid/*"}]}`, `{"deniedMcpServers":[{"serverCommand":["node","x"]},{"serverUrl":"https://api.example.invalid/*"}]}`} {
		add(map[string]any{"action": "policy", "servers": servers, "settings": parse(t, cfg)})
	}
	add(map[string]any{"action": "policy", "servers": servers, "settings": parse(t, `{"allowedMcpServers":[{"serverName":"one"}],"deniedMcpServers":[{"serverName":"remote"}]}`), "policy": parse(t, `{"allowManagedMcpServersOnly":true,"allowedMcpServers":[{"serverUrl":"https://*.example.invalid/*"}]}`)})
	for _, cfg := range []string{`{}`, `{"disabledMcpjsonServers":["Peer.Name"],"enabledMcpjsonServers":["Peer_Name"]}`, `{"enabledMcpjsonServers":["Peer.Name"]}`, `{"enableAllProjectMcpServers":true}`, `{"skipDangerousModePermissionPrompt":true}`} {
		for _, interactive := range []bool{true, false} {
			add(map[string]any{"action": "approval", "name": "Peer_Name", "settings": parse(t, cfg), "nonInteractive": interactive})
		}
	}
	add(map[string]any{"action": "approval", "name": "Peer_Name", "settings": parse(t, `{"skipDangerousModePermissionPrompt":true}`), "sources": []string{}, "nonInteractive": true})
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source MCP configuration comparison requires pinned Bun")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	cmd := exec.Command(bun, "run", filepath.Join(root, "scripts/quality-gate/go-parity/native-mcp-config.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_MCP_CONFIG_FIXTURE=1"}
	input, _ := json.Marshal(map[string]any{"cases": cases})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source MCP config: %v\n%s", err, stderr.String())
	}
	expected, _ := json.Marshal(observations)
	var a, b []any
	if json.Unmarshal(data, &a) != nil || json.Unmarshal(expected, &b) != nil {
		t.Fatalf("invalid observations %s", data)
	}
	if !reflect.DeepEqual(a, b) {
		for i := range a {
			if i >= len(b) || !reflect.DeepEqual(a[i], b[i]) {
				actual, _ := json.Marshal(a[i])
				want, _ := json.Marshal(b[i])
				t.Fatalf("case %d %v\nSource %s\nGo %s", i, cases[i], actual, want)
			}
		}
		t.Fatal("observations mismatch")
	}
}
