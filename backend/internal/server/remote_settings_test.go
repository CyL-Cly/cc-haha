package server

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
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestRemoteSettingsAndCredentialsActualSourceRouter(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	options.Store = storage.New(storage.Options{})
	options.Environment = func() map[string]string { return map[string]string{"CLAUDE_CONFIG_DIR": options.ConfigDir} }
	configured := settings.Options{Store: options.Store, HomeDir: options.HomeDir, ConfigDir: options.ConfigDir, Environment: options.Environment, EnsureMigrated: func(context.Context, string) error { return nil }, ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }, GitIgnore: func(context.Context, string, string) error { return nil }}
	var err error
	options.Settings, err = settings.New(configured)
	if err != nil {
		t.Fatal(err)
	}
	options.Managed, err = settings.NewManaged(configured)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	host := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		server.remoteSettingsHTTP(writer, request, segments(request.URL.EscapedPath()))
	}))
	defer host.Close()
	index := json.RawMessage(`{"schemaVersion":6,"activeId":null,"providers":[{"id":"fixture","presetId":"custom","name":"Fixture","apiKey":"fake-existing-secret","baseUrl":"https://trusted.invalid","apiFormat":"anthropic","runtimeKind":"anthropic_compatible","models":{"main":"fixture","haiku":"fixture","sonnet":"fixture","opus":"fixture"},"imageGeneration":{"model":"image","apiKey":"fake-image-secret"},"requestCompatibility":{"maxOutputTokens":2048,"hiddenFuture":{"keep":true}},"futureToken":"fake-hidden-secret"}],"providerOrder":["fixture","claude-official","openai-official","grok-official"],"officialProviderModels":{},"futureRoot":{"keep":true}}`)
	user := json.RawMessage(`{"language":"en","alwaysThinkingEnabled":true,"agentTeamsEnabled":false,"autoQuestion":{"enabled":true,"timeoutMinutes":17,"private":true},"env":{"PRIVATE_KEY":"fake-env-secret"},"future":{"keep":true}}`)
	if err = os.MkdirAll(filepath.Join(options.ConfigDir, "cc-haha"), 0700); err != nil {
		t.Fatal(err)
	}
	cases := []map[string]any{
		{"path": "/api/providers"}, {"path": "/api/providers/fixture"}, {"path": "/api/settings/user"},
		{"path": "/api/providers/settings"}, {"path": "/api/providers/fixture/models"}, {"path": "/api/providers/test", "method": "POST", "body": "{}"}, {"path": "/api/settings/project"}, {"path": "/api/settings/user/extra"},
		{"path": "/api/settings/user", "method": "PUT", "body": `{"language":"","outputStyle":"Learning","chatSendBehavior":"modifierEnter","alwaysThinkingEnabled":false,"workflowKeywordTriggerEnabled":true,"autoQuestion":{"enabled":false,"timeoutMinutes":30}}`},
		{"path": "/api/settings/user", "method": "PUT", "body": `{"env":{"API_KEY":"fake-forbidden"}}`},
		{"path": "/api/settings/user", "method": "PUT", "body": `{"autoQuestion":{"enabled":true,"timeoutMinutes":5,"extra":1}}`},
		{"path": "/api/settings/user", "method": "PUT", "body": `{"alwaysThinkingEnabled":"true"}`},
		{"path": "/api/settings/user", "method": "PUT", "body": `{"language":"` + strings.Repeat("界", 81) + `"}`},
		{"path": "/api/settings/user", "method": "PUT", "body": `{"language":"` + strings.Repeat("🚀", 41) + `"}`},
		{"path": "/api/settings/user", "method": "PUT", "body": "null"}, {"path": "/api/settings/user", "method": "PUT", "body": "[]"}, {"path": "/api/settings/user", "method": "PUT", "body": "{broken"},
		{"path": "/api/providers/fixture", "method": "PUT", "body": `{"name":"Renamed","apiKey":"","imageGeneration":{"model":"updated","apiKey":""},"requestCompatibility":{"sampling":"unsupported","hiddenFuture":"overwrite","newHidden":"never"}}`},
		{"path": "/api/providers/fixture", "method": "PUT", "body": `{"requestCompatibility":null}`},
		{"path": "/api/providers/fixture", "method": "PUT", "body": `{"requestCompatibility":{}}`},
		{"path": "/api/providers/fixture", "method": "PUT", "body": `{"imageGeneration":{"model":"updated"}}`},
		{"path": "/api/providers/fixture", "method": "PUT", "body": `{"baseUrl":"https://new.invalid","apiKey":""}`},
		{"path": "/api/providers/fixture", "method": "PUT", "body": `{"imageGeneration":{"model":"updated","baseUrl":"https://new.invalid"}}`},
		{"path": "/api/providers/fixture", "method": "PUT", "body": `{"baseUrl":"https://new.invalid","apiKey":"fake-new-secret","imageGeneration":{"model":"updated","apiKey":"fake-new-image"}}`},
		{"path": "/api/providers/unknown", "method": "PUT", "body": "{}"},
		{"path": "/api/providers/fixture", "method": "DELETE"},
	}
	observations := []any{}
	for _, row := range cases {
		if err = os.WriteFile(filepath.Join(options.ConfigDir, "settings.json"), user, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(options.ConfigDir, "cc-haha", "providers.json"), index, 0600); err != nil {
			t.Fatal(err)
		}
		method, _ := row["method"].(string)
		if method == "" {
			method = "GET"
		}
		body, _ := row["body"].(string)
		req, _ := http.NewRequest(method, host.URL+row["path"].(string), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"fake-existing-secret", "fake-image-secret", "fake-hidden-secret", "fake-env-secret", "fake-new-secret", "fake-new-image"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("remote response exposed a credential", row["path"])
			}
		}
		var response any
		if err = json.Unmarshal(data, &response); err != nil {
			t.Fatal(err, string(data))
		}
		read := func(name string) any {
			data, err := os.ReadFile(filepath.Join(options.ConfigDir, name))
			if err != nil {
				t.Fatal(err)
			}
			var v any
			if json.Unmarshal(data, &v) != nil {
				t.Fatal(string(data))
			}
			return v
		}
		observations = append(observations, map[string]any{"status": float64(res.StatusCode), "body": response, "settings": read("settings.json"), "index": read("cc-haha/providers.json")})
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Actual Source comparison requires pinned Bun")
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-remote-settings.fixture.ts"))
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = options.HomeDir
	cmd.Env = []string{"PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "HOME=" + options.HomeDir, "USERPROFILE=" + options.HomeDir, "CLAUDE_CONFIG_DIR=" + options.ConfigDir, "TMPDIR=" + options.HomeDir, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1"}
	input, _ := json.Marshal(map[string]any{"cases": cases, "index": index, "settings": user})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source remote settings %v\n%s", err, stderr.String())
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
