package clibootstrap

import (
	"os"
	"runtime"
	"strings"
)

func nativeAnalyticsArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	}
	return runtime.GOARCH
}
func nativeAnalyticsDeployment(env map[string]string, n *NativeBoundary) string {
	for _, row := range []struct {
		kind, key, second string
		truthy            bool
	}{
		{"codespaces", "CODESPACES", "", true}, {"gitpod", "GITPOD_WORKSPACE_ID", "", false}, {"replit", "REPL_ID", "REPL_SLUG", false}, {"glitch", "PROJECT_DOMAIN", "", false}, {"vercel", "VERCEL", "", true}, {"railway", "RAILWAY_ENVIRONMENT_NAME", "RAILWAY_SERVICE_NAME", false}, {"render", "RENDER", "", true}, {"netlify", "NETLIFY", "", true}, {"heroku", "DYNO", "", false}, {"fly.io", "FLY_APP_NAME", "FLY_MACHINE_ID", false}, {"cloudflare-pages", "CF_PAGES", "", true}, {"deno-deploy", "DENO_DEPLOYMENT_ID", "", false}, {"aws-lambda", "AWS_LAMBDA_FUNCTION_NAME", "", false},
	} {
		enabled := env[row.key] != "" || env[row.second] != ""
		if row.truthy {
			enabled = envTruthyNative(env[row.key])
		}
		if enabled {
			return row.kind
		}
	}
	if env["AWS_EXECUTION_ENV"] == "AWS_ECS_FARGATE" {
		return "aws-fargate"
	}
	if env["AWS_EXECUTION_ENV"] == "AWS_ECS_EC2" {
		return "aws-ecs"
	}
	if n.check("analytics metadata", "/sys/hypervisor/uuid") == nil {
		if raw, e := os.ReadFile("/sys/hypervisor/uuid"); e == nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(raw))), "ec2") {
			return "aws-ec2"
		}
	}
	for _, row := range []struct {
		kind, key, second string
		truthy            bool
	}{
		{"gcp-cloud-run", "K_SERVICE", "", false}, {"gcp", "GOOGLE_CLOUD_PROJECT", "", false}, {"azure-app-service", "WEBSITE_SITE_NAME", "WEBSITE_SKU", false}, {"azure-functions", "AZURE_FUNCTIONS_ENVIRONMENT", "", false},
	} {
		if env[row.key] != "" || env[row.second] != "" {
			return row.kind
		}
	}
	if strings.Contains(env["APP_URL"], "ondigitalocean.app") {
		return "digitalocean-app-platform"
	}
	if env["SPACE_CREATOR_USER_ID"] != "" {
		return "huggingface-spaces"
	}
	for _, row := range []struct {
		kind, key string
		truthy    bool
	}{{"github-actions", "GITHUB_ACTIONS", true}, {"gitlab-ci", "GITLAB_CI", true}, {"circleci", "CIRCLECI", false}, {"buildkite", "BUILDKITE", false}, {"ci", "CI", true}, {"kubernetes", "KUBERNETES_SERVICE_HOST", false}} {
		enabled := env[row.key] != ""
		if row.truthy {
			enabled = envTruthyNative(env[row.key])
		}
		if enabled {
			return row.kind
		}
	}
	if n.check("analytics metadata", "/.dockerenv") == nil {
		if _, e := os.Stat("/.dockerenv"); e == nil {
			return "docker"
		}
	}
	switch runtime.GOOS {
	case "darwin", "linux":
		return "unknown-" + runtime.GOOS
	case "windows":
		return "unknown-win32"
	}
	return "unknown"
}
func nativeAnalyticsTerminal(env map[string]string, stdoutTTY bool) *string {
	value := ""
	if env["CURSOR_TRACE_ID"] != "" {
		value = "cursor"
	}
	for _, kind := range []string{"cursor", "windsurf", "antigravity"} {
		if value == "" && strings.Contains(env["VSCODE_GIT_ASKPASS_MAIN"], kind) {
			value = kind
		}
	}
	bundle := strings.ToLower(env["__CFBundleIdentifier"])
	for _, row := range []struct{ match, kind string }{{"vscodium", "codium"}, {"windsurf", "windsurf"}, {"com.google.android.studio", "androidstudio"}} {
		if value == "" && strings.Contains(bundle, row.match) {
			value = row.kind
		}
	}
	for _, ide := range []string{"pycharm", "intellij", "webstorm", "phpstorm", "rubymine", "clion", "goland", "rider", "datagrip", "appcode", "dataspell", "aqua", "gateway", "fleet", "jetbrains", "androidstudio"} {
		if value == "" && strings.Contains(bundle, ide) {
			value = ide
		}
	}
	if value == "" && env["VisualStudioVersion"] != "" {
		value = "visualstudio"
	}
	if value == "" && env["TERMINAL_EMULATOR"] == "JetBrains-JediTerm" {
		value = "pycharm"
	}
	if value == "" && env["TERM"] == "xterm-ghostty" {
		value = "ghostty"
	}
	if value == "" && strings.Contains(env["TERM"], "kitty") {
		value = "kitty"
	}
	if value == "" {
		value = env["TERM_PROGRAM"]
	}
	for _, row := range []struct{ key, kind string }{{"TMUX", "tmux"}, {"STY", "screen"}, {"KONSOLE_VERSION", "konsole"}, {"GNOME_TERMINAL_SERVICE", "gnome-terminal"}, {"XTERM_VERSION", "xterm"}, {"VTE_VERSION", "vte-based"}, {"TERMINATOR_UUID", "terminator"}, {"KITTY_WINDOW_ID", "kitty"}, {"ALACRITTY_LOG", "alacritty"}, {"TILIX_ID", "tilix"}, {"WT_SESSION", "windows-terminal"}} {
		if value == "" && env[row.key] != "" {
			value = row.kind
		}
	}
	if value == "" && env["SESSIONNAME"] != "" && env["TERM"] == "cygwin" {
		value = "cygwin"
	}
	if value == "" && env["MSYSTEM"] != "" {
		value = strings.ToLower(env["MSYSTEM"])
	}
	if value == "" && (env["ConEmuANSI"] != "" || env["ConEmuPID"] != "" || env["ConEmuTask"] != "") {
		value = "conemu"
	}
	if value == "" && env["WSL_DISTRO_NAME"] != "" {
		value = "wsl-" + env["WSL_DISTRO_NAME"]
	}
	if value == "" && (env["SSH_CONNECTION"] != "" || env["SSH_CLIENT"] != "" || env["SSH_TTY"] != "") {
		value = "ssh-session"
	}
	if value == "" && env["TERM"] != "" {
		value = env["TERM"]
		for _, kind := range []string{"alacritty", "rxvt", "termite"} {
			if strings.Contains(env["TERM"], kind) {
				value = kind
				break
			}
		}
	}
	if value == "" && !stdoutTTY {
		value = "non-interactive"
	}
	if value == "" {
		return nil
	}
	return &value
}
