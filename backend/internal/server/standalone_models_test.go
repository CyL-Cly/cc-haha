package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestStandaloneModelsAndAuthActualSource(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	if err := os.MkdirAll(options.ConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	options.Environment = func() map[string]string { return env }
	options.Store = storage.New(storage.Options{})
	var err error
	options.Settings, err = settings.New(settings.Options{Store: options.Store, HomeDir: options.HomeDir, ConfigDir: options.ConfigDir, Environment: options.Environment, GitExecutable: "git", EnsureMigrated: func(context.Context, string) error { return nil }, ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	options.HahaOAuth, err = desktopoauth.New(desktopoauth.Options{Home: options.HomeDir, ConfigDir: options.ConfigDir, Environment: options.Environment, Store: options.Store, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = options.HahaOAuth.Close() })
	_, host := testHost(t, options)
	cases := []map[string]any{
		{"path": "/api/models"}, {"path": "/api/models/current"}, {"path": "/api/providers/auth-status"},
		{"path": "/api/models", "env": map[string]string{"ANTHROPIC_MODEL": " deepseek-flash[1m] ", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "deepseek-flash[1m]", "ANTHROPIC_DEFAULT_SONNET_MODEL": "other-model"}, "settings": map[string]any{"env": map[string]any{"ANTHROPIC_MODEL": "settings-model", "ANTHROPIC_DEFAULT_OPUS_MODEL": "settings-opus", "ANTHROPIC_DEFAULT_FABLE_MODEL": 12}}},
		{"path": "/api/models/current", "env": map[string]string{"ANTHROPIC_MODEL": " deepseek-flash[1m] "}},
		{"path": "/api/models/current", "env": map[string]string{"ANTHROPIC_MODEL": " runtime "}, "settings": map[string]any{"model": "explicit", "modelContext": "1m"}},
		{"path": "/api/models/current", "settings": map[string]any{"env": map[string]any{"ANTHROPIC_MODEL": " settings-main "}}},
		{"path": "/api/models", "env": map[string]string{"ANTHROPIC_MODEL": "   "}, "settings": map[string]any{"env": map[string]any{"ANTHROPIC_MODEL": " settings-main ", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "settings-main"}}},
		{"path": "/api/models", "settings": map[string]any{"env": []string{"invalid shape"}}},
		{"path": "/api/models/current", "method": "PUT", "body": `{"modelId":"deepseek-flash[1m]:1m"}`, "settings": map[string]any{"future": map[string]any{"keep": true}}},
		{"path": "/api/models/current", "method": "PUT", "body": `{"modelId":"new-model"}`, "settings": map[string]any{"modelContext": "1m", "future": true}},
		{"path": "/api/providers/auth-status", "env": map[string]string{"ANTHROPIC_AUTH_TOKEN": "fake-provider-test"}},
		{"path": "/api/providers/auth-status", "settings": map[string]any{"env": map[string]string{"ANTHROPIC_API_KEY": "fake-settings-test"}}},
		{"path": "/api/models/current", "settings": map[string]any{"model": "claude-opus-5-5", "modelContext": "200k"}},
		{"path": "/api/models/current", "method": "DELETE"}, {"path": "/api/models/current", "method": "PUT", "body": `{"modelId":12}`},
	}
	for _, row := range []map[string]any{
		{"path": "/api/models"}, {"path": "/api/models/current"}, {"path": "/api/providers/auth-status"},
		{"path": "/api/models/current", "settings": map[string]any{"model": "opus[1m]"}},
		{"path": "/api/models/current", "settings": map[string]any{"model": "sonnet:1m", "modelContext": "1m"}},
		{"path": "/api/models/current", "env": map[string]string{"ANTHROPIC_MODEL": "deepseek-flash"}},
	} {
		row["oauth"] = map[string]any{"accessToken": "fake-desktop-oauth", "refreshToken": nil, "expiresAt": nil, "scopes": []string{}, "subscriptionType": "max"}
		cases = append(cases, row)
	}
	observations := []any{}
	for _, row := range cases {
		oauthPath := filepath.Join(options.ConfigDir, "cc-haha", "oauth.json")
		if err = os.MkdirAll(filepath.Dir(oauthPath), 0700); err != nil {
			t.Fatal(err)
		}
		if value, ok := row["oauth"]; ok {
			data, _ := json.Marshal(value)
			if err = os.WriteFile(oauthPath, data, 0600); err != nil {
				t.Fatal(err)
			}
		} else if err = os.Remove(oauthPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		env = map[string]string{}
		if values, ok := row["env"].(map[string]string); ok {
			env = values
		}
		values := row["settings"]
		if values == nil {
			values = map[string]any{}
		}
		data, _ := json.Marshal(values)
		if err = os.WriteFile(filepath.Join(options.ConfigDir, "settings.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		method, _ := row["method"].(string)
		if method == "" {
			method = "GET"
		}
		body, _ := row["body"].(string)
		req, _ := http.NewRequest(method, host.URL+row["path"].(string), bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		response, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		responseData, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err = json.Unmarshal(responseData, &value); err != nil {
			t.Fatal(err, string(responseData))
		}
		settingsValue, err := options.Settings.GetUserSettings(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		data, _ = json.Marshal(map[string]any{"status": response.StatusCode, "body": value, "settings": settingsValue})
		var observation any
		_ = json.Unmarshal(data, &observation)
		observations = append(observations, observation)
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Actual Source comparison requires pinned Bun")
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-standalone-models.fixture.ts"))
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = options.HomeDir
	cmd.Env = []string{"PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "HOME=" + options.HomeDir, "USERPROFILE=" + options.HomeDir, "CLAUDE_CONFIG_DIR=" + options.ConfigDir, "TMPDIR=" + options.HomeDir, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1"}
	data, _ := json.Marshal(map[string]any{"cases": cases})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source models: %v\n%s", err, stderr.String())
	}
	var expected []any
	if err = json.Unmarshal(source, &expected); err != nil {
		t.Fatal(err, string(source))
	}
	for i, actual := range observations {
		if !reflect.DeepEqual(actual, expected[i]) {
			t.Fatalf("case %d %v\nSource %v\nGo %v", i, cases[i], expected[i], actual)
		}
	}
}
