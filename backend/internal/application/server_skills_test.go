package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopSkillsHTTPBindsActualPluginAndNativeRoots(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	config, home := o.Environment["CLAUDE_CONFIG_DIR"], o.Environment["HOME"]
	o.WorkDir = filepath.Join(home, "work")
	if err := os.MkdirAll(o.WorkDir, 0700); err != nil {
		t.Fatal(err)
	}
	market := filepath.Join(o.WorkDir, "market")
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(config, "settings.json"), `{"enabledPlugins":{"sample@mkt":true},"future":{"keep":true}}`)
	write(filepath.Join(config, "skills", "user", "SKILL.md"), "---\ndescription: Actual user skill\n---\nUser instructions.")
	write(filepath.Join(home, ".agents", "skills", "user", "SKILL.md"), "Shadowed shared skill.")
	write(filepath.Join(home, ".agents", "skills", "shared", "SKILL.md"), "Shared-only skill.")
	write(filepath.Join(o.WorkDir, ".claude", "skills", "project", "SKILL.md"), "Project skill.")
	write(filepath.Join(market, "plugins", "sample", ".claude-plugin", "plugin.json"), `{"name":"sample"}`)
	write(filepath.Join(market, "plugins", "sample", "skills", "plugin", "SKILL.md"), "---\ndescription: Actual plugin skill\n---\nPlugin instructions.")
	write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"mkt","owner":{"name":"Owner"},"plugins":[{"name":"sample","source":"./plugins/sample"}]}`)
	known, _ := json.Marshal(map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2026-10-01T00:00:00.000Z"}})
	write(filepath.Join(config, "plugins", "known_marketplaces.json"), string(known))
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	request := func(method, path, body string, status int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, host.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != status {
			t.Fatalf("%s %s: status=%d body=%s err=%v", method, path, resp.StatusCode, data, err)
		}
		if method == http.MethodGet || method == http.MethodPost {
			compareNativeSkillsSourceHTTP(t, home, config, o.WorkDir, method, path, status, data)
		}
		return data
	}
	list := request("GET", "/api/skills", "", 200)
	for _, name := range []string{"user", "shared", "project", "sample:plugin"} {
		if !bytes.Contains(list, []byte(`"name":"`+name+`"`)) {
			t.Fatalf("actual skill %q absent from %s", name, list)
		}
	}
	if bytes.Contains(list, []byte("Shadowed shared skill")) {
		t.Fatalf("root precedence changed: %s", list)
	}
	detail := request("GET", "/api/skills/detail?source=plugin&name=sample:plugin", "", 200)
	if !bytes.Contains(detail, []byte("Plugin instructions.")) || !bytes.Contains(detail, []byte(`"frontmatter"`)) {
		t.Fatalf("actual plugin detail absent: %s", detail)
	}
	request("GET", "/api/skills/detail?source=user&name=missing", "", 404)
	request("GET", "/api/skills/detail", "", 400)
	request("GET", "/api/skills/detail?source=unknown&name=user", "", 400)
	request("GET", "/api/skills/detail?source=user&name=..", "", 400)
	request("GET", "/api/skills/detail?source=user&name=a%2Fb", "", 400)
	request("POST", "/api/skills", `{}`, 405)
	request("GET", "/api/skills/unknown", "", 404)
	request("PUT", "/api/settings/user", `{"enabledPlugins":{"sample@mkt":false},"disableAgentSkillsDirectory":true}`, 200)
	list = request("GET", "/api/skills", "", 200)
	if bytes.Contains(list, []byte(`"name":"sample:plugin"`)) || bytes.Contains(list, []byte(`"name":"shared"`)) {
		t.Fatalf("disabled roots retained: %s", list)
	}
	request("GET", "/api/skills/detail?source=plugin&name=sample:plugin", "", 404)
	if _, err := os.Stat(filepath.Join(config, "projects")); err == nil {
		t.Fatal("Skills discovery started a hidden Agent")
	}
}

// Uses the actual Source handler and plugin/config/file producers over the same
// disposable post-request state. No loader, Skill service or API is mocked.
// This proves HTTP projections and cross-version readability; fresh install
// transactions and complete runtime Skill execution have their own boundaries.
func compareNativeSkillsSourceHTTP(t *testing.T, home, config, cwd, method, path string, status int, data []byte) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		return
	}
	cwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-plugin-settings.fixture.ts")
	input, err := json.Marshal(map[string]any{"runtime": map[string]any{"cwd": cwd}, "operations": []map[string]string{{"kind": "skillHTTP", "method": method, "path": path}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_PLUGIN_SETTINGS_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source Skills HTTP: %v %s", err, output)
	}
	var source struct {
		Observations []struct {
			Status int
			Body   string
		}
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Observations) != 1 {
		t.Fatalf("actual Source observations missing: %v %s", err, output)
	}
	var got, want any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(source.Observations[0].Body), &want); err != nil {
		t.Fatal(err)
	}
	if source.Observations[0].Status != status || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s %s differs from actual Source: Go %d %s; Source %d %s", method, path, status, data, source.Observations[0].Status, source.Observations[0].Body)
	}
}
