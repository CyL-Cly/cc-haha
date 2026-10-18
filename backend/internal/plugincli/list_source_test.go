package plugincli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExistingListProjectionAgainstActualSourcePhysicalInstalled(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(root, "home")
	cwd := home
	config := filepath.Join(home, ".claude")
	plugins := filepath.Join(config, "plugins")
	plugin := filepath.Join(plugins, "cache", "fixture", "alpha", "1.0")
	for _, dir := range []string{home, cwd, config, plugin, filepath.Join(plugin, ".claude-plugin")} {
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if e = os.WriteFile(path, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(filepath.Join(config, "settings.json"), `{"future":{"keep":true},"enabledPlugins":{"alpha@fixture":true}}`)
	write(filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"alpha","version":"1.0"}`)
	installed := protocol.NewJSONObject(testField("version", 2), testField("plugins", map[string]any{"alpha@fixture": []map[string]any{{"scope": "user", "installPath": plugin, "version": "1.0", "installedAt": "2024-01-01T00:00:00Z", "lastUpdated": "2024-01-01T00:00:00Z"}}}))
	data, _ := installed.MarshalJSON()
	write(filepath.Join(plugins, "installed_plugins.json"), string(data))
	fixture, _ := filepath.Abs("../../testdata/native-cli-plugins.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": root, "USER": "fixture", "PATH": "/usr/bin:/bin", "BUN_OPTIONS": "--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT": root, "CC_HAHA_PLUGIN_CLI_FIXTURE": "1", "CC_HAHA_PLUGIN_CLI_INPUT": `{"action":"list","options":{"json":true}}`, "NODE_ENV": "production", "USER_TYPE": "external", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}
	cmd := exec.Command("/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = cwd
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source plugin list", e, stderr.String())
	}
	n, e := newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: "darwin", ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	command, e := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: []string{"plugin", "list", "--json"}, Native: n, Platform: "darwin", PID: os.Getpid(), Version: "999.0.0-local", Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if e != nil {
		t.Fatal(e)
	}
	defer command.Close()
	e = command.Run(context.Background(), func(scope *cooperative.Scope) error {
		result, err := command.PluginCommand(context.Background(), scope, plugincli.Command{Action: "list", JSON: true})
		if err != nil {
			return err
		}
		if result.ExitCode != 0 || result.Stderr != "" {
			t.Fatal(result)
		}
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(source), protocol.StringifyValidatedJavaScriptJSON([]byte(result.Stdout))) {
			t.Fatalf("actual Source %s Go %s", source, result.Stdout)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}

}

func TestOriginalCLIListStartupModes(t *testing.T) {
	for _, mode := range []string{"human", "inline", "inline-human", "cowork", "available", "available-human", "empty", "disabled", "cowork-catalog", "mcp-inline", "add-dir"} {
		t.Run(mode, func(t *testing.T) { pairedListStartup(t, mode) })
	}
}
func pairedListStartup(t *testing.T, mode string) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(root, "home")
	cwd := home
	config := filepath.Join(home, ".claude")
	pluginDirName := "plugins"
	if mode == "cowork" || mode == "cowork-catalog" {
		pluginDirName = "cowork_plugins"
	}
	plugins := filepath.Join(config, pluginDirName)
	plugin := filepath.Join(plugins, "cache", "fixture", "alpha", "1.0")
	for _, dir := range []string{home, cwd, config, plugin, filepath.Join(plugin, ".claude-plugin")} {
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if e = os.WriteFile(path, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(filepath.Join(config, "settings.json"), `{"future":{"keep":true},"enabledPlugins":{"alpha@fixture":true}}`)
	write(filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"alpha","version":"1.0"}`)
	installed := protocol.NewJSONObject(testField("version", 2), testField("plugins", map[string]any{"alpha@fixture": []map[string]any{{"scope": "user", "installPath": plugin, "version": "1.0", "installedAt": "2024-01-01T00:00:00Z", "lastUpdated": "2024-01-01T00:00:00Z"}}}))
	data, _ := installed.MarshalJSON()
	write(filepath.Join(plugins, "installed_plugins.json"), string(data))
	args := []string{"plugin", "list", "--json"}
	input := plugincli.Command{Action: "list", JSON: true}
	sourceInput := map[string]any{"action": "list", "options": map[string]any{"json": true}}
	if mode == "human" {
		args = []string{"plugin", "list"}
		input.JSON = false
		sourceInput["options"] = map[string]any{}
	}
	if mode == "cowork" || mode == "cowork-catalog" {
		args = append(args, "--cowork")
		input.Cowork = true
		sourceInput["options"].(map[string]any)["cowork"] = true
	}
	if mode == "empty" {
		write(filepath.Join(plugins, "installed_plugins.json"), `{"version":2,"plugins":{}}`)
		write(filepath.Join(config, "settings.json"), `{"future":{"keep":true}}`)
		input.JSON = false
		args = []string{"plugin", "list"}
		sourceInput["options"] = map[string]any{}
	}
	if mode == "disabled" {
		write(filepath.Join(config, "settings.json"), `{"future":{"keep":true},"enabledPlugins":{"alpha@fixture":false}}`)
		input.JSON = false
		args = []string{"plugin", "list"}
		sourceInput["options"] = map[string]any{}
	}
	if mode == "available" || mode == "available-human" {
		market := filepath.Join(home, "market")
		_ = os.MkdirAll(filepath.Join(market, ".claude-plugin"), 0700)
		write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"fixture","owner":{"name":"Fixture"},"plugins":[{"name":"alpha","source":"./alpha"},{"name":"beta","source":"./beta","version":"2.0","description":"Available beta"}]}`)
		known, _ := json.Marshal(map[string]any{"fixture": map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2024-01-01T00:00:00Z"}, "broken": map[string]any{"source": map[string]string{"source": "directory", "path": filepath.Join(home, "absent")}, "installLocation": filepath.Join(home, "absent"), "lastUpdated": "2024-01-01T00:00:00Z"}})
		write(filepath.Join(plugins, "known_marketplaces.json"), string(known))
		write(filepath.Join(plugins, "install-counts-cache.json"), `{"version":1,"fetchedAt":"2099-01-01T00:00:00.000Z","counts":[{"plugin":"beta@fixture","unique_installs":137}]}`)
		input.Available = true
		args = append(args, "--available")
		sourceInput["options"].(map[string]any)["available"] = true
		if mode == "available-human" {
			input.JSON = false
			args = []string{"plugin", "list", "--available"}
			sourceInput["options"].(map[string]any)["json"] = false
		}
	}
	if mode == "cowork-catalog" {
		market := filepath.Join(home, "cowork-market")
		source := filepath.Join(market, "alpha")
		_ = os.MkdirAll(filepath.Join(market, ".claude-plugin"), 0700)
		_ = os.MkdirAll(filepath.Join(source, ".claude-plugin"), 0700)
		write(filepath.Join(source, ".claude-plugin", "plugin.json"), `{"name":"alpha","version":"1.0"}`)
		write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"fixture","owner":{"name":"Fixture"},"plugins":[{"name":"alpha","source":"./alpha","commands":"./cowork-missing.md"}]}`)
		known, _ := json.Marshal(map[string]any{"fixture": map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2024-01-01T00:00:00Z"}})
		write(filepath.Join(plugins, "known_marketplaces.json"), string(known))
	}
	additional := []string{}
	if mode == "add-dir" {
		dir := filepath.Join(home, "additional")
		_ = os.MkdirAll(filepath.Join(dir, ".claude"), 0700)
		write(filepath.Join(dir, ".claude/settings.json"), `{"enabledPlugins":{"alpha@fixture":true}}`)
		write(filepath.Join(config, "settings.json"), `{"future":{"keep":true}}`)
		args = append([]string{"--add-dir=" + dir}, args...)
		additional = []string{dir}
	}
	inlinePaths := []string{}
	if mode == "inline" || mode == "inline-human" || mode == "mcp-inline" {
		dir := filepath.Join(home, "dev-checkout")
		_ = os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0700)
		write(filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"alpha","version":"3.0","commands":"./missing-command.md"}`)
		if mode == "mcp-inline" {
			write(filepath.Join(dir, ".mcp.json"), `{"owned":{"command":"owned-fixture-command","args":["--root","${CLAUDE_PLUGIN_ROOT}"],"env":{"OWNED_PLUGIN_ROOT":"${CLAUDE_PLUGIN_ROOT}"}}}`)
		}
		inlinePaths = []string{dir, filepath.Join(home, "missing-inline")}
		prefix := []string{}
		for _, path := range inlinePaths {
			prefix = append(prefix, "--plugin-dir", path)
		}
		args = append(prefix, args...)
		if mode == "inline-human" {
			input.JSON = false
			args = []string{"--plugin-dir", inlinePaths[0], "--plugin-dir", inlinePaths[1], "plugin", "list"}
			sourceInput["options"] = map[string]any{}
		}
	}
	fixture, _ := filepath.Abs("../../testdata/native-cli-plugins.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": root, "USER": "fixture", "PATH": "/usr/bin:/bin", "BUN_OPTIONS": "--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT": root, "CC_HAHA_PLUGIN_CLI_FIXTURE": "1", "CC_HAHA_PLUGIN_CLI_INPUT": `{"action":"list","options":{"json":true}}`, "NODE_ENV": "production", "USER_TYPE": "external", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}
	payload, _ := json.Marshal(sourceInput)
	env["CC_HAHA_PLUGIN_CLI_INPUT"] = string(payload)
	paths, _ := json.Marshal(inlinePaths)
	env["CC_HAHA_PLUGIN_CLI_INLINE"] = string(paths)
	additionalJSON, _ := json.Marshal(additional)
	env["CC_HAHA_PLUGIN_CLI_ADDITIONAL"] = string(additionalJSON)
	cmd := exec.Command("/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = cwd
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source plugin list", e, stderr.String())
	}
	n, e := newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: "darwin", ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	command, e := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: args, Native: n, Platform: "darwin", PID: os.Getpid(), Version: "999.0.0-local", Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if e != nil {
		t.Fatal(e)
	}
	defer command.Close()
	var result plugincli.Result
	e = command.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		result, err = command.PluginCommand(context.Background(), scope, input)
		return err
	})
	if e != nil {
		t.Fatal(e)
	}
	if result.ExitCode != 0 || result.Stderr != "" {
		t.Fatal(result)
	}
	equal := bytes.Equal(source, []byte(result.Stdout))
	if input.JSON {
		equal = bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(source), protocol.StringifyValidatedJavaScriptJSON([]byte(result.Stdout)))
	}
	if !equal {
		t.Fatalf("actual Source %s Go %s", source, result.Stdout)
	}

}

func testField(name string, value any) protocol.JSONField {
	raw, _ := json.Marshal(value)
	return protocol.JSONField{Name: name, Value: raw}
}
