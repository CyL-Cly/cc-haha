package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestPublicPluginAllOriginalHandlersPrivateProcesses(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	binary := filepath.Join(root, "public-go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal("actual public build", err, string(output))
	}
	setup := func(name string) (string, []string) {
		home := filepath.Join(root, name)
		config := filepath.Join(home, ".claude")
		market := filepath.Join(home, "owned-market")
		plugin := filepath.Join(market, "alpha")
		for _, dir := range []string{config, filepath.Join(market, ".claude-plugin"), filepath.Join(plugin, ".claude-plugin")} {
			if err = os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
		write := func(path, body string) {
			if err = os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		write(filepath.Join(config, "settings.json"), `{"future":{"keep":"owned"}}`)
		write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"fixture","owner":{"name":"Fixture"},"plugins":[{"name":"alpha","source":"./alpha","version":"1.0"}]}`)
		write(filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"alpha","version":"1.0","description":"Fixture","author":{"name":"Owned"}}`)
		write(filepath.Join(plugin, "physical.txt"), "public actual materialization\n")
		for _, pair := range []struct{ name, body string }{{"owned-warnings", `{"name":"WarningPlugin"}`}, {"owned-invalid", `{"name":"alpha","typo":true}`}} {
			dir := filepath.Join(home, pair.name, ".claude-plugin")
			_ = os.MkdirAll(dir, 0700)
			write(filepath.Join(dir, "plugin.json"), pair.body)
		}
		if err = os.Symlink("owned-loop", filepath.Join(home, "owned-loop")); err != nil {
			t.Fatal(err)
		}
		env := []string{"HOME=" + home, "USERPROFILE=" + home, "USER=owned-fixture", "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_PLUGIN_CLI_FIXTURE=1", "PATH=/usr/bin:/bin:/opt/homebrew/bin", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1"}
		git := exec.Command("/usr/bin/git", "init", "-q", home)
		git.Env = env
		if output, err := git.CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
		return home, env
	}
	sourceHome, sourceEnv := setup("source-home")
	goHome, goEnv := setup("go-home")
	fixture, _ := filepath.Abs("../../testdata/native-cli-plugins.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	cases := []struct {
		action, argument string
		options          map[string]any
		args             []string
	}{
		{"list", "", nil, []string{"plugin", "list"}},
		{"marketplaceAdd", "owned-market", nil, []string{"plugin", "marketplace", "add", "./owned-market"}},
		{"marketplaceList", "", nil, []string{"plugin", "marketplace", "list"}},
		{"marketplaceList", "", map[string]any{"json": true}, []string{"plugin", "marketplace", "list", "--json"}},
		{"validate", "owned-market/alpha", nil, []string{"plugin", "validate", "./owned-market/alpha"}},
		{"validate", "./owned-warnings", nil, []string{"plugin", "validate", "./owned-warnings"}},
		{"validate", "./owned-invalid", nil, []string{"plugin", "validate", "./owned-invalid"}},
		{"validate", "./owned-loop/plugin.json", nil, []string{"plugin", "validate", "./owned-loop/plugin.json"}},
		{"validate", "owned-market/alpha", map[string]any{"cowork": true}, []string{"plugin", "validate", "./owned-market/alpha", "--cowork"}},
		{"install", "alpha@fixture", map[string]any{"scope": "local"}, []string{"plugin", "install", "alpha@fixture", "--scope", "local"}},
		{"list", "", map[string]any{"json": true}, []string{"plugin", "list", "--json"}},
		{"list", "", map[string]any{"cowork": true, "json": true}, []string{"plugin", "list", "--cowork", "--json"}},
		{"disable", "alpha@fixture", nil, []string{"plugin", "disable", "alpha@fixture"}},
		{"enable", "alpha@fixture", nil, []string{"plugin", "enable", "alpha@fixture"}},
		{"disable", "", map[string]any{"all": true}, []string{"plugin", "disable", "--all"}},
		{"update", "alpha@fixture", map[string]any{"scope": "local"}, []string{"plugin", "update", "alpha@fixture", "--scope", "local"}},
		{"uninstall", "alpha@fixture", map[string]any{"scope": "local", "keepData": true}, []string{"plugin", "uninstall", "alpha@fixture", "--scope", "local", "--keep-data"}},
		{"marketplaceUpdate", "fixture", nil, []string{"plugin", "marketplace", "update", "fixture"}},
		{"marketplaceUpdate", "", nil, []string{"plugin", "marketplace", "update"}},
		{"marketplaceRemove", "fixture", nil, []string{"plugin", "marketplace", "remove", "fixture"}},
		{"marketplaceList", "", nil, []string{"plugin", "marketplace", "list"}},
		{"disable", "alpha@fixture", map[string]any{"all": true}, []string{"plugin", "disable", "alpha@fixture", "--all"}},
	}
	run := func(home string, env []string, program string, args ...string) (int, string, string) {
		t.Helper()
		process := exec.CommandContext(ctx, program, args...)
		process.Dir = home
		process.Env = env
		var stdout, stderr bytes.Buffer
		process.Stdout, process.Stderr = &stdout, &stderr
		code := 0
		if err := process.Run(); err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		return code, stdout.String(), stderr.String()
	}
	normalize := func(s, home string) string {
		return regexp.MustCompile(`"(installedAt|lastUpdated)": "[^"]+"`).ReplaceAllString(strings.ReplaceAll(s, home, "<HOME>"), `"$1": "<CLOCK>"`)
	}
	for index, item := range cases {
		t.Run(item.action+"-"+strings.Join(item.args, "_"), func(t *testing.T) {
			argument := item.argument
			if strings.HasPrefix(argument, "owned-market") {
				argument = "./" + argument
			}
			input, _ := json.Marshal(map[string]any{"action": item.action, "argument": argument, "options": item.options})
			env := append(append([]string{}, sourceEnv...), "CC_HAHA_PLUGIN_CLI_INPUT="+string(input))
			sourceCode, sourceOut, sourceErr := run(sourceHome, env, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
			code, out, diagnostics := run(goHome, goEnv, binary, item.args...)
			if code != sourceCode || normalize(out, goHome) != normalize(sourceOut, sourceHome) || normalize(diagnostics, goHome) != normalize(sourceErr, sourceHome) {
				t.Fatalf("public case%d actual Source exit%d stdout%q stderr%q Go exit%d stdout%q stderr%q", index, sourceCode, sourceOut, sourceErr, code, out, diagnostics)
			}
			for _, rel := range []string{".claude/settings.json", ".claude/settings.local.json", ".claude/plugins/known_marketplaces.json", ".claude/plugins/installed_plugins.json"} {
				read := func(home string) any {
					data, e := os.ReadFile(filepath.Join(home, rel))
					if os.IsNotExist(e) {
						return nil
					}
					if e != nil {
						t.Fatal(e)
					}
					var value any
					if e = json.Unmarshal([]byte(normalize(string(data), home)), &value); e != nil {
						t.Fatal(e)
					}
					normalizePublicPluginState(value)
					return value
				}
				a, b := read(sourceHome), read(goHome)
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("public physical %s Source%v Go%v", rel, a, b)
				}
			}
		})
	}
	for _, home := range []string{sourceHome, goHome} {
		data, e := os.ReadFile(filepath.Join(home, ".config", "git", "ignore"))
		if e != nil || !strings.Contains(string(data), "**/.claude/settings.local.json") {
			t.Fatal("public actual GitIgnore", e, string(data))
		}
	}
}
func normalizePublicPluginState(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "installedAt" || key == "lastUpdated" {
				delete(v, key)
			} else {
				normalizePublicPluginState(child)
			}
		}
	case []any:
		for _, child := range v {
			normalizePublicPluginState(child)
		}
	}
}
