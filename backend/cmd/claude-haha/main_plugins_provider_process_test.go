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

func TestPublicPluginMarketplaceWithSavedProviderPrivateProcesses(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "public-go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal("compiled public CLI", e, string(out))
	}
	setup := func(name string) (string, []string) {
		home := filepath.Join(root, name)
		config := filepath.Join(home, ".claude")
		market := filepath.Join(home, "owned-market")
		for _, dir := range []string{filepath.Join(config, "cc-haha"), filepath.Join(market, ".claude-plugin")} {
			if e = os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
		}
		write := func(path, body string) {
			if e = os.WriteFile(path, []byte(body), 0600); e != nil {
				t.Fatal(e)
			}
		}
		write(filepath.Join(config, "settings.json"), `{"future":{"keep":"owned"}}`)
		write(filepath.Join(config, "cc-haha/providers.json"), `{"activeId":"owned-provider","providers":[{"id":"owned-provider","name":"Owned","presetId":"","baseUrl":"http://127.0.0.1:1","apiKey":"fake-private-provider","apiFormat":"openai","models":{"main":"owned-model","haiku":"owned-model","sonnet":"owned-model","opus":"owned-model"}}],"futureIndex":{"keep":true}}`)
		write(filepath.Join(market, ".claude-plugin/marketplace.json"), `{"name":"owned-market","owner":{"name":"Owned"},"plugins":[]}`)
		env := []string{"HOME=" + home, "USERPROFILE=" + home, "USER=owned", "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_PLUGIN_CLI_FIXTURE=1", "PATH=/usr/bin:/bin:/opt/homebrew/bin", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1"}
		git := exec.Command("/usr/bin/git", "init", "-q", home)
		git.Env = env
		if out, e := git.CombinedOutput(); e != nil {
			t.Fatal(e, string(out))
		}
		return home, env
	}
	sourceHome, sourceEnv := setup("source-home")
	goHome, goEnv := setup("go-home")
	fixture, _ := filepath.Abs("../../testdata/native-cli-plugins.fixture.ts")
	run := func(home string, env []string, program string, args ...string) (string, string, error) {
		child := exec.CommandContext(ctx, program, args...)
		child.Dir = home
		child.Env = env
		var out, diagnostic bytes.Buffer
		child.Stdout, child.Stderr = &out, &diagnostic
		e := child.Run()
		return out.String(), diagnostic.String(), e
	}
	input, _ := json.Marshal(map[string]any{"action": "marketplaceAdd", "argument": "./owned-market"})
	sourceEnv = append(sourceEnv, "CC_HAHA_PLUGIN_CLI_INPUT="+string(input))
	original, originalErr, e := run(sourceHome, sourceEnv, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	if e != nil {
		t.Fatal("actual Source marketplace", e, originalErr)
	}
	actual, actualErr, e := run(goHome, goEnv, binary, "plugin", "marketplace", "add", "./owned-market")
	if e != nil {
		t.Fatalf("ordinary public CLI with saved provider: %v stderr=%s; Source stdout=%s stderr=%s", e, actualErr, original, originalErr)
	}
	normalize := func(text, home string) string { return strings.ReplaceAll(text, home, "<HOME>") }
	if normalize(actual, goHome) != normalize(original, sourceHome) || normalize(actualErr, goHome) != normalize(originalErr, sourceHome) {
		t.Fatalf("Source stdout=%s stderr=%s; Go stdout=%s stderr=%s", original, originalErr, actual, actualErr)
	}
	installed, e := os.ReadFile(filepath.Join(goHome, ".claude/plugins/known_marketplaces.json"))
	if e != nil || !bytes.Contains(installed, []byte(`"owned-market"`)) {
		t.Fatal("actual CLI did not persist owned marketplace", e, string(installed))
	}
	before, e := os.ReadFile(filepath.Join(goHome, ".claude/cc-haha/providers.json"))
	if e != nil || !bytes.Contains(before, []byte(`"futureIndex":{"keep":true}`)) {
		t.Fatal("provider index changed", e, string(before))
	}
	output, diagnostic, e := run(goHome, goEnv, binary, "plugin", "marketplace", "list", "--json")
	if e != nil || !strings.Contains(output, "owned-market") {
		t.Fatal("restarted public CLI", e, output, diagnostic)
	}
}
