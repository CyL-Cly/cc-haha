package clibootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActualSourceAnalyticsTerminalAndDeploymentFacts(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun unavailable")
	}
	keys := strings.Fields("CURSOR_TRACE_ID VSCODE_GIT_ASKPASS_MAIN __CFBundleIdentifier VisualStudioVersion TERMINAL_EMULATOR TERM TERM_PROGRAM TMUX STY KONSOLE_VERSION GNOME_TERMINAL_SERVICE XTERM_VERSION VTE_VERSION TERMINATOR_UUID KITTY_WINDOW_ID ALACRITTY_LOG TILIX_ID WT_SESSION SESSIONNAME MSYSTEM ConEmuANSI ConEmuPID ConEmuTask WSL_DISTRO_NAME SSH_CONNECTION SSH_CLIENT SSH_TTY CODESPACES GITPOD_WORKSPACE_ID REPL_ID REPL_SLUG PROJECT_DOMAIN VERCEL RAILWAY_ENVIRONMENT_NAME RAILWAY_SERVICE_NAME RENDER NETLIFY DYNO FLY_APP_NAME FLY_MACHINE_ID CF_PAGES DENO_DEPLOYMENT_ID AWS_LAMBDA_FUNCTION_NAME AWS_EXECUTION_ENV K_SERVICE GOOGLE_CLOUD_PROJECT WEBSITE_SITE_NAME WEBSITE_SKU AZURE_FUNCTIONS_ENVIRONMENT APP_URL SPACE_CREATOR_USER_ID GITHUB_ACTIONS GITLAB_CI CIRCLECI BUILDKITE CI KUBERNETES_SERVICE_HOST")
	cases := []map[string]string{{}, {"TERM": "xterm-ghostty", "TERM_PROGRAM": "vscode"}, {"TERM": "xterm-kitty", "TERM_PROGRAM": "vscode"}, {"CURSOR_TRACE_ID": "1", "TERM_PROGRAM": "iTerm.app"}, {"MSYSTEM": "MINGW64"}, {"__CFBundleIdentifier": "com.jetbrains.goland"}, {"VSCODE_GIT_ASKPASS_MAIN": "/private/windsurf/askpass"}, {"APP_URL": "https://private.ondigitalocean.app"}, {"AWS_EXECUTION_ENV": "AWS_ECS_FARGATE"}, {"AWS_EXECUTION_ENV": "AWS_ECS_EC2"}, {"TERM": "xterm-rxvt"}, {"SESSIONNAME": "private", "TERM": "cygwin"}, {"TERMINAL_EMULATOR": "JetBrains-JediTerm"}}
	for _, key := range keys {
		cases = append(cases, map[string]string{key: "true"}, map[string]string{key: "false"})
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	config := filepath.Join(home, ".claude")
	tmp := filepath.Join(home, "tmp")
	for _, p := range []string{home, config, tmp} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	root, _ := filepath.Abs("../../..")
	fixture := filepath.Join(root, "scripts/quality-gate/go-parity/native-file-analytics-environment.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "NODE_ENV=test", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"cases": cases, "keys": keys})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var want []struct {
		Terminal   *string `json:"terminal"`
		Deployment string  `json:"deployment"`
	}
	if e = json.Unmarshal(raw, &want); e != nil {
		t.Fatal(e, string(raw))
	}
	for i, env := range cases {
		got := struct {
			Terminal   *string `json:"terminal"`
			Deployment string  `json:"deployment"`
		}{nativeAnalyticsTerminal(env, false), nativeAnalyticsDeployment(env, &NativeBoundary{})}
		if !reflect.DeepEqual(got, want[i]) {
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(want[i])
			t.Errorf("profile%d env%v Go%s Source%s", i, env, a, b)
		}
	}
}
