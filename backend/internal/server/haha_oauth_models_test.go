package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestHahaOAuthProviderModelsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(filepath.Join(config, "cc-haha"), 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	store := storage.New(storage.Options{})
	options := settings.Options{Store: store, HomeDir: home, ConfigDir: config, GitExecutable: "/usr/bin/git", Environment: func() map[string]string { return map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config} }, EnsureMigrated: func(context.Context, string) error { return nil }, ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }}
	user, err := settings.New(options)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := settings.NewManaged(options)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := providerconfig.New(providerconfig.Options{Store: store, ConfigDir: config, Managed: managed, Settings: user})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{options: Options{Store: store, ConfigDir: config, Settings: user, Managed: managed}}
	type step struct {
		Method  string                    `json:"method"`
		Path    string                    `json:"path"`
		Body    *string                   `json:"body,omitempty"`
		Index   any                       `json:"index,omitempty"`
		User    any                       `json:"user,omitempty"`
		Managed any                       `json:"managed,omitempty"`
		Catalog []providerproxy.GrokModel `json:"catalog,omitempty"`
	}
	s := func(value string) *string { return &value }
	index := func(active any) map[string]any {
		return map[string]any{"schemaVersion": 6, "activeId": active, "providers": []any{}, "providerOrder": providerconfig.BuiltInIDs, "officialProviderModels": map[string]any{"future-provider": map[string]any{"unknown": true}}, "future": map[string]any{"preserved": "yes"}}
	}
	seeds := map[string]any{"unknown": map[string]any{"preserved": true}, "model": "old-model", "modelContext": "1m", "env": map[string]any{"USER_OWNED": "preserved", "ANTHROPIC_API_KEY": "fake-other-key", "ANTHROPIC_DEFAULT_FABLE_MODEL": "old-fable"}}
	valid := `{"models":{"main":"  custom-model  ","fable":" ","haiku":" ","sonnet":" custom-sonnet ","opus":"","ignored":true},"ignored":true}`
	items := []step{
		{Method: "GET", Path: "/api/providers/claude-official/models", Index: index(nil), User: seeds, Managed: seeds},
		{Method: "GET", Path: "/api/providers/openai-official/models"},
		{Method: "GET", Path: "/api/providers/grok-official/models"},
		{Method: "PUT", Path: "/api/providers/claude-official/models", Body: s(valid)},
		{Method: "GET", Path: "/api/providers/claude-official/models"},
		{Method: "PUT", Path: "/api/providers/claude-official/models", Body: s(`{"models":{"main":"new","fable":" \ufeff claude-fable-5-1 \u00a0","haiku":"h","sonnet":"s","opus":"o"}}`), Index: index("openai-official"), User: seeds},
		{Method: "PUT", Path: "/api/providers/openai-official/models", Body: s(valid), Index: index("openai-official"), Managed: seeds},
		{Method: "PUT", Path: "/api/providers/grok-official/models", Body: s(valid), Index: index("grok-official"), Managed: seeds},
		{Method: "PUT", Path: "/api/providers/openai-official/models", Body: s(valid), Index: index("grok-official"), Managed: seeds},
		{Method: "PUT", Path: "/api/providers/missing/models", Body: s(valid)},
		{Method: "GET", Path: "/api/providers/missing/models"},
		{Method: "POST", Path: "/api/providers/missing/models", Body: s(valid)},
	}
	items = append(items, step{Method: "PUT", Path: "/api/providers/grok-official/models", Body: s(valid), Index: index("grok-official"), Managed: seeds, Catalog: []providerproxy.GrokModel{{Value: "grok-4.7", ContextWindow: 600001}, {Value: "future-window", ContextWindow: 1234.8}}})
	for _, body := range []string{`{`, `null`, `[]`, `false`, `{}`, `{"models":null}`, `{"models":[]}`, `{"models":{}}`, `{"models":{"main":"  ","fable":null,"haiku":3,"sonnet":true,"opus":[]}}`, `{"models":{"main":3,"haiku":"h","sonnet":"s","opus":"o"}}`} {
		items = append(items, step{Method: "PUT", Path: "/api/providers/claude-official/models", Body: s(body)})
	}
	read := func(path string) any {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var result any
		_ = json.Unmarshal(raw, &result)
		return result
	}
	actual := []any{}
	for _, item := range items {
		server.options.GrokRuntimeModelCatalog = func() []providerproxy.GrokModel {
			if item.Catalog != nil {
				return item.Catalog
			}
			return providerproxy.GrokModelCatalog()
		}
		for _, seed := range []struct {
			value any
			path  string
		}{{item.Index, filepath.Join(config, "cc-haha", "providers.json")}, {item.User, filepath.Join(config, "settings.json")}, {item.Managed, filepath.Join(config, "cc-haha", "settings.json")}} {
			if seed.value != nil {
				raw, _ := json.Marshal(seed.value)
				if err = os.WriteFile(seed.path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
		body := ""
		if item.Body != nil {
			body = *item.Body
		}
		req := httptest.NewRequest(item.Method, "http://localhost:3456"+item.Path, strings.NewReader(body))
		parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
		writer := httptest.NewRecorder()
		if err = server.officialProviderModelsHTTP(writer, req, parts[2], provider); err != nil {
			writeError(writer, err)
		}
		var response any
		if json.Unmarshal(writer.Body.Bytes(), &response) != nil {
			t.Fatal("invalid Go response")
		}
		actual = append(actual, map[string]any{"status": writer.Code, "body": response, "index": read(filepath.Join(config, "cc-haha", "providers.json")), "user": read(filepath.Join(config, "settings.json")), "managed": read(filepath.Join(config, "cc-haha", "settings.json"))})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-oauth.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_DESKTOP_OAUTH_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	raw, _ := json.Marshal(map[string]any{"clock": 1770000000000, "providerModels": items})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source mapping %v %.1500s", err, stderr.String())
	}
	actualJSON, _ := json.Marshal(actual)
	var goValues, sourceValues []any
	_ = json.Unmarshal(actualJSON, &goValues)
	if json.Unmarshal(output, &sourceValues) != nil {
		t.Fatalf("Source JSON %.1500s", output)
	}
	if len(goValues) != len(sourceValues) {
		t.Fatal("Source result count")
	}
	for i, value := range goValues {
		if !reflect.DeepEqual(value, sourceValues[i]) {
			got, _ := json.Marshal(value)
			want, _ := json.Marshal(sourceValues[i])
			t.Errorf("case%d Go=%.6000s Source=%.6000s", i, got, want)
		}
	}
}
