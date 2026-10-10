package imruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestActualSourceIMWorkspaceEnvironmentCanonicalRootsAndGUIFallback(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "projects", "repo")
	second := filepath.Join(home, "second")
	outside := t.TempDir()
	for _, dir := range []string{repo, second} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(home, "repo-link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	profiles := []map[string]any{}
	add := func(file any, env map[string]string, cwd string) {
		value := any(nil)
		if file != nil {
			value = string(raw(file))
		}
		profiles = append(profiles, map[string]any{"file": value, "env": env, "cwd": cwd})
	}
	add(nil, map[string]string{}, "/")
	add(map[string]any{}, map[string]string{"PWD": "/", "ADAPTER_DEFAULT_PROJECT_DIR": "relative", "CLAUDE_ADAPTER_DEFAULT_WORK_DIR": repo}, "/")
	add(map[string]any{"defaultProjectDir": repo, "telegram": map[string]any{"defaultWorkDir": second, "botToken": "private-file"}, "serverUrl": "ws://localhost:1"}, map[string]string{}, "/")
	add(map[string]any{"telegram": map[string]any{"defaultWorkDir": repo, "botToken": "private-file"}}, map[string]string{"ADAPTER_SERVER_URL": "ws://127.0.0.1:2", "TELEGRAM_BOT_TOKEN": "private-env"}, "/")
	add(map[string]any{"defaultProjectDir": outside, "allowedProjectRoots": []string{repo}}, map[string]string{}, "/")
	add(map[string]any{"defaultProjectDir": outside, "allowedProjectRoots": []string{repo}, "telegram": map[string]any{"allowedProjectRoots": []string{second}}}, map[string]string{"ADAPTER_ALLOWED_PROJECT_ROOTS": link + string(os.PathListSeparator) + repo + string(os.PathListSeparator) + "/missing"}, "/")
	add(map[string]any{"defaultProjectDir": repo, "telegram": map[string]any{"allowedProjectRoots": []string{"/missing", "relative", ""}}}, map[string]string{}, "/")
	add(map[string]any{"telegram": map[string]any{"defaultWorkDir": repo, "allowedProjectRoots": []string{second}}}, map[string]string{"ADAPTER_ALLOWED_PROJECT_ROOTS": ":"}, "/")
	add(map[string]any{"defaultProjectDir": "/", "allowedProjectRoots": []string{"/"}}, map[string]string{}, "/")
	add(map[string]any{"defaultProjectDir": "~/projects/repo", "allowedProjectRoots": []any{7, "", nil, "~", "~/projects"}}, map[string]string{}, "/")
	add(map[string]any{}, map[string]string{"PWD": repo}, "/")
	add(map[string]any{}, map[string]string{}, second)
	add("legacy primitive", map[string]string{}, "/")
	add(false, map[string]string{}, "/")
	add([]any{}, map[string]string{}, "/")
	add(map[string]any{"telegram": map[string]any{"botToken": 7}}, map[string]string{}, "/")
	want := runIMSource(t, "native-im-config-workspace.fixture.ts", "CC_HAHA_IM_WORKSPACE_FIXTURE", map[string]any{"profiles": profiles}, home)
	rows := []any{}
	config := filepath.Join(home, ".claude")
	for _, profile := range profiles {
		if profile["file"] == nil {
			_ = os.Remove(filepath.Join(config, "adapters.json"))
		} else if err := os.WriteFile(filepath.Join(config, "adapters.json"), []byte(profile["file"].(string)), 0600); err != nil {
			t.Fatal(err)
		}
		warnings := []string{}
		env := map[string]string{"CLAUDE_CONFIG_DIR": config}
		for k, v := range profile["env"].(map[string]string) {
			env[k] = v
		}
		c := LoadConfig(home, profile["cwd"].(string), env, func(s string) { warnings = append(warnings, s) })
		rows = append(rows, map[string]any{"workspace": map[string]any{"defaultWorkDir": c.WorkDir, "allowedProjectRoots": c.Roots}, "serverUrl": c.ServerURL, "token": c.Token, "warnings": warnings})
	}
	exactJSON(t, raw(rows), json.RawMessage(want), "workspace")
}

func TestActualOriginalSlackConfigWorkspaceAndBothCredentialAuthorityPrecedence(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	second := filepath.Join(home, "second")
	_ = os.MkdirAll(repo, 0700)
	_ = os.MkdirAll(second, 0700)
	configDir := filepath.Join(home, ".claude")
	_ = os.MkdirAll(configDir, 0700)
	profiles := []map[string]any{
		{"file": string(raw(map[string]any{"telegram": map[string]any{"defaultWorkDir": second, "botToken": "unrelated"}, "slack": map[string]any{"defaultWorkDir": repo, "botToken": "private-file-bot", "appToken": "private-file-app", "allowedProjectRoots": []string{repo}}})), "env": map[string]string{}, "cwd": "/"},
		{"file": string(raw(map[string]any{"defaultProjectDir": repo, "slack": map[string]any{"defaultWorkDir": second, "botToken": "file-bot", "appToken": "file-app"}})), "env": map[string]string{"SLACK_BOT_TOKEN": "env-bot", "SLACK_APP_TOKEN": "env-app", "ADAPTER_SERVER_URL": "wss://private.test"}, "cwd": "/"},
		{"file": string(raw(map[string]any{"slack": map[string]any{"botToken": 7, "appToken": true}})), "env": map[string]string{"PWD": repo}, "cwd": "/"},
		{"file": string(raw(map[string]any{"defaultProjectDir": repo, "slack": map[string]any{"defaultWorkDir": second, "allowedProjectRoots": []string{second}}})), "env": map[string]string{}, "cwd": "/"},
	}
	want := runIMSource(t, "native-im-config-workspace.fixture.ts", "CC_HAHA_IM_WORKSPACE_FIXTURE", map[string]any{"platform": "slack", "profiles": profiles}, home)
	rows := []any{}
	for _, p := range profiles {
		_ = os.WriteFile(filepath.Join(configDir, "adapters.json"), []byte(p["file"].(string)), 0600)
		env := map[string]string{"CLAUDE_CONFIG_DIR": configDir}
		for k, v := range p["env"].(map[string]string) {
			env[k] = v
		}
		warnings := []string{}
		c := LoadPlatformConfig("slack", home, p["cwd"].(string), env, func(s string) { warnings = append(warnings, s) })
		rows = append(rows, map[string]any{"workspace": map[string]any{"defaultWorkDir": c.WorkDir, "allowedProjectRoots": c.Roots}, "serverUrl": c.ServerURL, "token": c.Token, "appToken": SlackAppToken(c), "warnings": warnings})
	}
	exactJSON(t, raw(rows), want, "actual Slack config/workspace")
}
