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

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestDesktopOpenAIModelsAPIActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source comparison requires pinned Bun")
	}
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("model API started a model session")
		return nil, nil
	})
	o.Store = storage.New(storage.Options{})
	options := settings.Options{Store: o.Store, HomeDir: o.HomeDir, ConfigDir: o.ConfigDir, Environment: func() map[string]string { return map[string]string{} }, EnsureMigrated: func(context.Context, string) error { return nil }, ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }}
	var err error
	o.Managed, err = settings.NewManaged(options)
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{{"path": "/api/models"}}
	for _, managed := range []any{map[string]any{}, map[string]any{"model": "gpt-6-astra"}, map[string]any{"model": "gpt-5.4", "modelContext": "1m"}, map[string]any{"env": map[string]string{"ANTHROPIC_MODEL": "gpt-6.1-sol"}}, map[string]any{"model": "unknown-custom", "modelContext": "wide"}} {
		rows = append(rows, map[string]any{"path": "/api/models/current", "managed": managed})
	}
	for _, body := range []string{`{"modelId":"gpt-6-luna:200k"}`, `{"modelId":"gpt-6-sol"}`, `{"modelId":3}`} {
		rows = append(rows, map[string]any{"path": "/api/models/current", "method": "PUT", "body": body, "managed": map[string]any{"modelContext": "1m", "future": true, "env": map[string]string{"USER_OWNED": "keep"}}})
	}
	remote := []providerproxy.OpenAIModel{{Value: "remote-model", Label: "Remote", Description: "private catalog fixture", DefaultReasoningEffort: "high", SupportedReasoningEfforts: []string{}, ContextWindow: 123456}}
	rows = append(rows, map[string]any{"path": "/api/models", "catalog": remote}, map[string]any{"path": "/api/models/current", "catalog": remote, "managed": map[string]string{"model": "remote-model"}})
	for _, raw := range []string{
		`{"value":"\ud800","label":"\udc00","description":"\ud800 text","defaultReasoningEffort":"low","supportedReasoningEfforts":["low"],"contextWindow":1e100}`,
		`{"value":"remote-null-window","label":"Remote","description":"","defaultReasoningEffort":"medium","supportedReasoningEfforts":[],"contextWindow":null}`,
		`{"value":"remote-negative-window","label":"Remote","description":"","defaultReasoningEffort":"high","supportedReasoningEfforts":["high"],"contextWindow":-17}`,
	} {
		var model providerproxy.OpenAIModel
		if err = json.Unmarshal([]byte(raw), &model); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, map[string]any{"path": "/api/models", "catalog": []providerproxy.OpenAIModel{model}})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-desktop-models.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = o.HomeDir
	cmd.Env = []string{"HOME=" + o.HomeDir, "USERPROFILE=" + o.HomeDir, "CLAUDE_CONFIG_DIR=" + o.ConfigDir, "TMPDIR=" + o.HomeDir, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(o.HomeDir), "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source model API %v\n%s", err, stderr.String())
	}
	var expected []any
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source model API output %s", output)
	}
	var originalObservations []protocol.JSONObject
	if err = json.Unmarshal(output, &originalObservations); err != nil {
		t.Fatal(err)
	}
	o.Authorization = access.Authorization{LocalToken: "fixture-local"}
	var currentCatalog []providerproxy.OpenAIModel
	calls := 0
	o.OpenAIModelCatalog = func(context.Context) ([]providerproxy.OpenAIModel, error) { calls++; return currentCatalog, nil }
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for i, row := range rows {
		data, _ := json.Marshal(row["managed"])
		if row["managed"] == nil {
			data = []byte(`{}`)
		}
		if err = os.WriteFile(filepath.Join(o.ConfigDir, "cc-haha", "settings.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		currentCatalog = providerproxy.OpenAIModelCatalog()
		if catalog, ok := row["catalog"].([]providerproxy.OpenAIModel); ok {
			currentCatalog = catalog
		}
		calls = 0
		method, _ := row["method"].(string)
		if method == "" {
			method = "GET"
		}
		body, _ := row["body"].(string)
		request := httptest.NewRequest(method, "http://localhost:3456"+row["path"].(string), strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer fixture-local")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, request)
		var value any
		if json.Unmarshal(w.Body.Bytes(), &value) != nil {
			t.Fatal(w.Body.String())
		}
		managed, err := o.Managed.ReadSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		data, _ = json.Marshal(map[string]any{"status": w.Code, "body": value, "catalogCalls": calls, "managed": managed})
		var actual any
		_ = json.Unmarshal(data, &actual)
		if !reflect.DeepEqual(actual, expected[i]) {
			t.Errorf("row %d Source %v Go %s", i, expected[i], data)
		}
		if row["path"] == "/api/models" {
			var actualBody, sourceBody protocol.JSONObject
			if err = actualBody.UnmarshalJSON(w.Body.Bytes()); err != nil {
				t.Fatal(err)
			}
			if err = sourceBody.UnmarshalJSON(originalObservations[i].Get("body")); err != nil {
				t.Fatal(err)
			}
			var actualModels, sourceModels []protocol.JSONObject
			if err = json.Unmarshal(actualBody.Get("models"), &actualModels); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(sourceBody.Get("models"), &sourceModels); err != nil {
				t.Fatal(err)
			}
			if len(actualModels) != len(sourceModels) {
				t.Fatal("catalog projection count")
			}
			for n, model := range actualModels {
				for _, field := range []string{"id", "name", "description", "context", "defaultReasoningEffort", "supportedReasoningEfforts"} {
					got := protocol.StringifyValidatedJavaScriptJSON(model.Get(field))
					want := protocol.StringifyValidatedJavaScriptJSON(sourceModels[n].Get(field))
					if !bytes.Equal(got, want) {
						t.Errorf("row %d model %d exact %s Source %s Go %s", i, n, field, want, got)
					}
				}
			}
		}
	}
}
