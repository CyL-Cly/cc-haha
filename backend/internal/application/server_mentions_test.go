package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopMentionsUsesActualCachedSkillsPluginMCPAndSelectedProvider(t *testing.T) {
	var models atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { models.Add(1); http.Error(w, "unexpected model", 500) }))
	defer upstream.Close()
	o := nativeSessionOptions(t, upstream.URL)
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	o.WorkDir = filepath.Join(canonicalHome, "work")
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(config, "settings.json"), `{"enabledPlugins":{"sample@mkt":true},"future":{"preserve":true}}`)
	write(filepath.Join(o.WorkDir, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(filepath.Join(config, "skills", "user", "SKILL.md"), "---\nname: Exact UI Name\ndescription: Never inject this description into modelText\n---\nActual user prompt.")
	write(filepath.Join(config, "skills", "simplify", "SKILL.md"), "---\ndescription: Disk simplify shadowed by bundled\n---\nActual disk prompt.")
	write(filepath.Join(config, "skills", "private", "SKILL.md"), "---\nuser-invocable: false\ndescription: Hidden prompt\n---\nPrivate instructions.")
	write(filepath.Join(config, "skills", "disabled", "SKILL.md"), "---\ndisable-model-invocation: true\ndescription: Non-callable prompt\n---\nNon-callable instructions.")
	write(filepath.Join(o.WorkDir, ".claude", "skills", "project", "SKILL.md"), "---\ndescription: Actual project skill\n---\nProject instructions.")
	market := filepath.Join(o.WorkDir, "market")
	plugin := filepath.Join(market, "plugins", "sample")
	write(filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"sample","description":"Actual sample plugin"}`)
	write(filepath.Join(plugin, "skills", "check", "SKILL.md"), "---\ndescription: Actual cached plugin skill\n---\nCached plugin instructions.")
	sentinel := filepath.Join(home, "mcp-was-executed")
	mcpConfig, _ := json.Marshal(map[string]any{"service": map[string]any{"command": "/bin/sh", "args": []string{"-c", "touch '" + sentinel + "'"}}})
	write(filepath.Join(plugin, ".mcp.json"), string(mcpConfig))
	write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"mkt","owner":{"name":"Owner"},"plugins":[{"name":"sample","source":"./plugins/sample"}]}`)
	known, _ := json.Marshal(map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2026-10-01T00:00:00.000Z"}})
	write(filepath.Join(config, "plugins", "known_marketplaces.json"), string(known))
	installed, _ := json.Marshal(map[string]any{"version": 2, "plugins": map[string]any{"sample@mkt": []any{map[string]any{"scope": "user", "version": "1.0", "installPath": plugin, "installedAt": "2026-10-01T00:00:00.000Z", "lastUpdated": "2026-10-01T00:00:00.000Z"}}}})
	write(filepath.Join(config, "plugins", "installed_plugins.json"), string(installed))
	provider := map[string]any{"id": "owned-image", "name": "Owned image", "presetId": "custom", "apiKey": "offline-never-transmitted", "baseUrl": upstream.URL, "apiFormat": "openai_responses", "models": map[string]string{"main": "fixture-model", "haiku": "fixture-model", "sonnet": "fixture-model", "opus": "fixture-model"}, "imageGeneration": map[string]string{"model": "gpt-image-2"}}
	index, _ := json.Marshal(map[string]any{"schemaVersion": 6, "activeId": "owned-image", "providers": []any{provider}, "providerOrder": []string{"owned-image"}})
	write(filepath.Join(config, "cc-haha", "providers.json"), string(index))
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	paths := []string{"/api/skills/mentions", "/api/skills/mentions?providerId=claude-official", "/api/skills/mentions?providerId=missing", "/api/skills/mentions?providerId=owned-image", "/api/skills/mentions?providerId=openai-official", "/api/skills/mentions?providerId=grok-official", "/api/skills/mentions?cwd=" + url.QueryEscape(o.WorkDir)}
	read := func(path string) []byte {
		t.Helper()
		response, err := host.Client().Get(host.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("%s status%d %s %v", path, response.StatusCode, body, err)
		}
		return body
	}
	actual := [][]byte{}
	for _, path := range paths {
		actual = append(actual, read(path))
	}
	compareDesktopMentionsSource(t, home, config, o.WorkDir, paths, actual)
	if models.Load() != 0 {
		t.Fatal("mention discovery submitted model request", models.Load())
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("mention discovery executed MCP", err)
	}
	if !bytes.Contains(actual[0], []byte(`"id":"sample:check"`)) || !bytes.Contains(actual[0], []byte(`plugin:sample:service`)) || !bytes.Contains(actual[0], []byte(`"id":"imagegen"`)) {
		t.Fatalf("actual callable identities missing: %s", actual[0])
	}
	if bytes.Contains(actual[1], []byte(`"id":"imagegen"`)) || bytes.Contains(actual[2], []byte(`"id":"imagegen"`)) {
		t.Fatal("unselected provider image capability leaked")
	}
	// A fresh request must observe actual edited disk and enabled-plugin state.
	write(filepath.Join(config, "skills", "user", "SKILL.md"), "---\nname: Changed UI Name\ndescription: Updated disk definition\n---\nFresh user prompt.")
	request, _ := http.NewRequest(http.MethodPut, host.URL+"/api/settings/user", bytes.NewBufferString(`{"enabledPlugins":{"sample@mkt":false}}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("actual settings disable %d %s", response.StatusCode, body)
	}
	fresh := read(paths[1])
	compareDesktopMentionsSource(t, home, config, o.WorkDir, []string{paths[1]}, [][]byte{fresh})
	if bytes.Contains(fresh, []byte(`"id":"sample:check"`)) || !bytes.Contains(fresh, []byte("Changed UI Name")) {
		t.Fatalf("fresh settings and definitions not applied: %s", fresh)
	}
}

func compareDesktopMentionsSource(t *testing.T, home, config, cwd string, paths []string, actual [][]byte) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-capability-mentions.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_CAPABILITY_MENTIONS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "ANTHROPIC_API_KEY=offline-unused-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"cwd": cwd, "paths": paths})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source mentions %v %.5000s", err, output)
	}
	var source struct {
		Results []struct {
			Status int
			Body   json.RawMessage
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(actual) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("invalid actual Source %v rows=%d blocked=%v %.1000s", err, len(source.Results), source.Capabilities.Blocked, output)
	}
	for i, result := range source.Results {
		var left, right any
		_ = json.Unmarshal(actual[i], &left)
		_ = json.Unmarshal(result.Body, &right)
		if result.Status != 200 || !reflect.DeepEqual(left, right) {
			t.Errorf("%s Go=%s Source%d=%s", paths[i], actual[i], result.Status, result.Body)
		}
	}
}
