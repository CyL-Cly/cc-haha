package plugincli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOriginalCLIPluginOperationsPhysicalSettingsAndRegistry(t *testing.T) {
	for _, action := range []string{"install", "disable", "enable", "disable-all", "uninstall", "update"} {
		t.Run(action, func(t *testing.T) { pairedPluginOperation(t, action, "user") })
	}
}
func TestOriginalCLIPluginInstallDependencyClosureAndFailures(t *testing.T) {
	for _, action := range []string{"install-deps", "install-cycle", "install-missing-dep", "install-blocked-dep", "install-missing", "install-cross"} {
		t.Run(action, func(t *testing.T) { pairedPluginOperation(t, action, "user") })
	}
}
func TestOriginalCLIPluginLocalGitInstallation(t *testing.T) {
	pairedPluginOperation(t, "install", "local")
}

func pairedPluginOperation(t *testing.T, action, scopeName string) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	setup := func(name string) (string, map[string]string) {
		home := filepath.Join(root, name)
		config := filepath.Join(home, ".claude")
		market := filepath.Join(home, "market")
		source := filepath.Join(market, "alpha")
		cache := filepath.Join(config, "plugins", "cache", "fixture", "alpha", "1.0")
		for _, dir := range []string{config, filepath.Join(market, ".claude-plugin"), filepath.Join(source, ".claude-plugin"), filepath.Join(cache, ".claude-plugin")} {
			_ = os.MkdirAll(dir, 0700)
		}
		write := func(path, body string) {
			if e := os.WriteFile(path, []byte(body), 0600); e != nil {
				t.Fatal(e)
			}
		}
		write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"fixture","owner":{"name":"Fixture"},"plugins":[{"name":"alpha","source":"./alpha","version":"1.0"}]}`)
		manifest := `{"name":"alpha","version":"1.0"}`
		if action == "update" {
			manifest = `{"name":"alpha","version":"2.0"}`
		}
		write(filepath.Join(source, ".claude-plugin", "plugin.json"), manifest)
		write(filepath.Join(source, "physical.txt"), "original physical plugin source\n")
		write(filepath.Join(cache, ".claude-plugin", "plugin.json"), `{"name":"alpha","version":"1.0"}`)
		known := map[string]any{"fixture": map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2024-01-01T00:00:00Z"}}
		raw, _ := json.Marshal(known)
		write(filepath.Join(config, "plugins", "known_marketplaces.json"), string(raw))
		if strings.HasPrefix(action, "install-") && action != "install-missing" {
			entry := map[string]any{"name": "alpha", "source": "./alpha", "version": "1.0", "dependencies": []string{"beta"}}
			beta := map[string]any{"name": "beta", "source": "./beta", "version": "1.0"}
			if action == "install-cycle" {
				beta["dependencies"] = []string{"alpha"}
			}
			if action == "install-cross" {
				entry["dependencies"] = []string{"beta@elsewhere"}
			}
			plugins := []any{entry}
			if action != "install-missing-dep" {
				plugins = append(plugins, beta)
			}
			catalog, _ := json.Marshal(map[string]any{"name": "fixture", "owner": map[string]string{"name": "Fixture"}, "plugins": plugins})
			write(filepath.Join(market, ".claude-plugin", "marketplace.json"), string(catalog))
			betaDir := filepath.Join(market, "beta")
			_ = os.MkdirAll(filepath.Join(betaDir, ".claude-plugin"), 0700)
			write(filepath.Join(betaDir, ".claude-plugin", "plugin.json"), `{"name":"beta","version":"1.0"}`)
			write(filepath.Join(betaDir, "physical.txt"), "actual dependency physical source\n")
		}
		settings := `{"future":{"keep":true},"enabledPlugins":{"alpha@fixture":true}}`
		if action == "enable" {
			settings = `{"future":{"keep":true},"enabledPlugins":{"alpha@fixture":false}}`
		}
		if strings.HasPrefix(action, "install") {
			settings = `{"future":{"keep":true}}`
		}
		write(filepath.Join(config, "settings.json"), settings)
		if scopeName == "local" {
			init := exec.Command("/usr/bin/git", "init", "-q", home)
			init.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1"}
			if output, e := init.CombinedOutput(); e != nil {
				t.Fatal(e, string(output))
			}
			write(filepath.Join(config, "settings.json"), `{"future":{"keep":true}}`)
		}
		if !strings.HasPrefix(action, "install") {
			raw, _ := json.Marshal(map[string]any{"version": 2, "plugins": map[string]any{"alpha@fixture": []map[string]any{{"scope": scopeName, "installPath": cache, "version": "1.0", "installedAt": "2024-01-01T00:00:00Z", "lastUpdated": "2024-01-01T00:00:00Z"}}}})
			write(filepath.Join(config, "plugins", "installed_plugins.json"), string(raw))
		}
		env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "USER": "fixture", "PATH": "/usr/bin:/bin:/opt/homebrew/bin", "BUN_OPTIONS": "--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT": root, "CC_HAHA_PLUGIN_CLI_FIXTURE": "1", "NODE_ENV": "production", "USER_TYPE": "external", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}
		if action == "install-blocked-dep" {
			env["CC_HAHA_PLUGIN_CLI_POLICY"] = `{"enabledPlugins":{"beta@fixture":false}}`
		}
		return home, env
	}
	sourceHome, sourceEnv := setup("source-home")
	goHome, goEnv := setup("go-home")
	inputAction := action
	if strings.HasPrefix(action, "install") {
		inputAction = "install"
	}
	options := map[string]any{"scope": scopeName}
	if action == "disable-all" {
		inputAction = "disable"
		options = map[string]any{"all": true}
	}
	input := map[string]any{"action": inputAction, "argument": "alpha@fixture", "options": options}
	if action == "disable-all" {
		delete(input, "argument")
	}
	if action == "install-missing" {
		input["argument"] = "missing@fixture"
	}
	payload, _ := json.Marshal(input)
	sourceEnv["CC_HAHA_PLUGIN_CLI_INPUT"] = string(payload)
	fixture, _ := filepath.Abs("../../testdata/native-cli-plugins.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = sourceHome
	for k, v := range sourceEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var sourceOut, sourceErr bytes.Buffer
	cmd.Stdout = &sourceOut
	cmd.Stderr = &sourceErr
	e = cmd.Run()
	sourceCode := 0
	if e != nil {
		var exit *exec.ExitError
		if !errors.As(e, &exit) {
			t.Fatal(e)
		}
		sourceCode = exit.ExitCode()
	}
	n, e := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: goEnv, CWD: goHome, Platform: "darwin", ManagedRoot: filepath.Join(goHome, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) {
		if policy := goEnv["CC_HAHA_PLUGIN_CLI_POLICY"]; policy != "" {
			var value protocol.JSONObject
			_ = value.UnmarshalJSON([]byte(policy))
			return &value, nil, nil
		}
		return nil, nil, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	command, e := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: []string{"plugin", "list"}, Native: n, Platform: "darwin", Version: "999.0.0-local", PID: os.Getpid(), Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if e != nil {
		t.Fatal(e)
	}
	defer command.Close()
	var result plugincli.Result
	e = command.Run(ctx, func(s *cooperative.Scope) error {
		var e error
		result, e = command.PluginCommand(ctx, s, plugincli.Command{Action: inputAction, Argument: func() string {
			if action == "disable-all" {
				return ""
			}
			if action == "install-missing" {
				return "missing@fixture"
			}
			return "alpha@fixture"
		}(), Scope: func() string {
			if action == "disable-all" {
				return ""
			}
			return scopeName
		}(), All: action == "disable-all"})
		return e
	})
	if e != nil || result.ExitCode != sourceCode || strings.ReplaceAll(result.Stdout, goHome, "<HOME>") != strings.ReplaceAll(sourceOut.String(), sourceHome, "<HOME>") || strings.ReplaceAll(result.Stderr, goHome, "<HOME>") != strings.ReplaceAll(sourceErr.String(), sourceHome, "<HOME>") {
		t.Fatalf("actual Source code%d stdout%q stderr%q Go error%v result%+v", sourceCode, sourceOut.String(), sourceErr.String(), e, result)
	}
	read := func(home, path string) any {
		raw, e := os.ReadFile(filepath.Join(home, path))
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			t.Fatal(e)
		}
		var value any
		if e = json.Unmarshal(raw, &value); e != nil {
			t.Fatal(e)
		}
		normalizePluginState(value, home)
		return value
	}
	for _, path := range []string{".claude/settings.json", ".claude/settings.local.json", ".claude/plugins/installed_plugins.json"} {
		source, actual := read(sourceHome, path), read(goHome, path)
		if !reflect.DeepEqual(source, actual) {
			t.Fatalf("physical original %s Source%v Go%v", path, source, actual)
		}
	}
	if scopeName == "local" {
		source, e := os.ReadFile(filepath.Join(sourceHome, ".config", "git", "ignore"))
		if e != nil {
			t.Fatal("Source actual asynchronous local ignore", e)
		}
		actual, e := os.ReadFile(filepath.Join(goHome, ".config", "git", "ignore"))
		if e != nil || !bytes.Equal(source, actual) {
			t.Fatalf("actual local GitIgnore Source%q Go%q error%v", source, actual, e)
		}
	}
	if action == "install" || action == "install-deps" || action == "update" {
		version := "1.0"
		if action == "update" {
			version = "2.0"
		}
		for _, home := range []string{sourceHome, goHome} {
			raw, e := os.ReadFile(filepath.Join(home, ".claude/plugins/cache/fixture/alpha", version, "physical.txt"))
			if e != nil || string(raw) != "original physical plugin source\n" {
				t.Fatal("actual materialized plugin file", e, string(raw))
			}
		}
	}
}
func normalizePluginState(value any, home string) {
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			if k == "installedAt" || k == "lastUpdated" {
				delete(v, k)
				continue
			}
			if text, ok := x.(string); ok {
				v[k] = strings.ReplaceAll(text, home, "<private-home>")
			} else {
				normalizePluginState(x, home)
			}
		}
	case []any:
		for _, x := range v {
			normalizePluginState(x, home)
		}
	}
}
