package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestProviderHTTPUsesSharedStorageAndSourceCRUDActivationContracts(t *testing.T) {
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
	_, host := testHost(t, options)
	presets := requestJSON(t, host, "GET", "/api/providers/presets", "", 200)
	var catalog struct{ Presets []json.RawMessage }
	_ = json.Unmarshal(presets, &catalog)
	if len(catalog.Presets) != 17 {
		t.Fatal("Source preset list changed")
	}
	assertProviderListShape := func(data []byte) {
		t.Helper()
		var body map[string]json.RawMessage
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		// Source listProviders retains order in storage but the GET handler
		// exposes only these two fields. Reorder has its own response contract.
		if len(body) != 2 || body["providers"] == nil || body["activeId"] == nil {
			t.Fatalf("Source provider list response fields changed: %s", data)
		}
	}
	assertProviderListShape(requestJSON(t, host, "GET", "/api/providers", "", 200))
	created := requestJSON(t, host, "POST", "/api/providers", `{"presetId":"deepseek","name":"Fixture DeepSeek","apiKey":"fake-http-provider-key","baseUrl":"http://127.0.0.1:1/anthropic","apiFormat":"anthropic","models":{"main":"deepseek-v4-flash","haiku":"","sonnet":"","opus":""}}`, 201)
	var result struct{ Provider map[string]json.RawMessage }
	_ = json.Unmarshal(created, &result)
	id := stringField(result.Provider, "id")
	if id == "" {
		t.Fatal("create omitted provider id")
	}
	assertProviderListShape(requestJSON(t, host, "GET", "/api/providers", "", 200))
	reorderInput, err := json.Marshal(map[string]any{"orderedIds": []string{id, "grok-official", "openai-official", "claude-official"}})
	if err != nil {
		t.Fatal(err)
	}
	reordered := requestJSON(t, host, "PUT", "/api/providers/reorder", string(reorderInput), 200)
	var orderResponse map[string]json.RawMessage
	if err := json.Unmarshal(reordered, &orderResponse); err != nil {
		t.Fatal(err)
	}
	if len(orderResponse) != 2 || orderResponse["providers"] == nil || orderResponse["providerOrder"] == nil {
		t.Fatalf("Source reorder response fields changed: %s", reordered)
	}
	assertProviderListShape(requestJSON(t, host, "GET", "/api/providers", "", 200))
	var orderedIndex struct{ ProviderOrder []string }
	indexPath := filepath.Join(options.ConfigDir, "cc-haha", "providers.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil || json.Unmarshal(indexData, &orderedIndex) != nil || len(orderedIndex.ProviderOrder) != 4 || orderedIndex.ProviderOrder[0] != id || orderedIndex.ProviderOrder[1] != "grok-official" {
		t.Fatal("list response repair changed persisted provider order", err, string(indexData))
	}
	requestJSON(t, host, "POST", "/api/providers/"+id+"/activate", "", 200)
	auth := requestJSON(t, host, "GET", "/api/providers/auth-status", "", 200)
	var status struct {
		HasAuth                bool
		Source, ActiveProvider string
	}
	_ = json.Unmarshal(auth, &status)
	if !status.HasAuth || status.Source != "cc-haha-provider" || status.ActiveProvider != "Fixture DeepSeek" {
		t.Fatal("selected provider auth status did not reflect actual saved provider")
	}
	models := requestJSON(t, host, "GET", "/api/models", "", 200)
	var listing struct{ Models []map[string]json.RawMessage }
	_ = json.Unmarshal(models, &listing)
	if len(listing.Models) != 1 || stringField(listing.Models[0], "id") != "deepseek-v4-flash" {
		t.Fatal("provider model slots not deduplicated")
	}
	current := requestJSON(t, host, "GET", "/api/models/current", "", 200)
	var selected struct{ Model map[string]json.RawMessage }
	_ = json.Unmarshal(current, &selected)
	if stringField(selected.Model, "id") != "deepseek-v4-flash" {
		t.Fatal("current model leaked global settings")
	}
	requestJSON(t, host, "PUT", "/api/models/current", `{"modelId":"fixture-custom:1m"}`, 200)
	current = requestJSON(t, host, "GET", "/api/models/current", "", 200)
	_ = json.Unmarshal(current, &selected)
	if stringField(selected.Model, "id") != "fixture-custom" || stringField(selected.Model, "context") != "1m" {
		t.Fatal("composite current model did not round trip")
	}
	requestJSON(t, host, "PUT", "/api/models/current", `{"modelId":"deepseek-v4-flash"}`, 200)
	requestJSON(t, host, "PUT", "/api/effort", `{"level":"high"}`, 200)
	effort := requestJSON(t, host, "GET", "/api/effort", "", 200)
	var effortResult struct{ Level string }
	_ = json.Unmarshal(effort, &effortResult)
	if effortResult.Level != "high" {
		t.Fatal("effort did not persist")
	}
	requestJSON(t, host, "PUT", "/api/effort", `{"level":"invalid"}`, 400)
	requestJSON(t, host, "PUT", "/api/providers/"+id, `{"notes":"updated","autoCompactWindow":16000}`, 200)
	updated := requestJSON(t, host, "GET", "/api/providers/"+id, "", 200)
	_ = json.Unmarshal(updated, &result)
	if stringField(result.Provider, "notes") != "updated" {
		t.Fatal("saved provider update not visible")
	}
	requestJSON(t, host, "DELETE", "/api/providers/"+id, "", 409)
	mapping := requestJSON(t, host, "PUT", "/api/permissions/mode", `{"mode":"acceptEdits"}`, 200)
	var mode struct {
		OK   bool
		Mode string
	}
	_ = json.Unmarshal(mapping, &mode)
	if !mode.OK || mode.Mode != "acceptEdits" {
		t.Fatal("Source permission mode response changed")
	}
	requestJSON(t, host, "POST", "/api/providers/official", "", 200)
	requestJSON(t, host, "DELETE", "/api/providers/"+id, "", 200)
	raw, err := os.ReadFile(filepath.Join(options.ConfigDir, "cc-haha", "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index struct{ Providers []json.RawMessage }
	if json.Unmarshal(raw, &index) != nil || len(index.Providers) != 0 {
		t.Fatal("HTTP delete did not persist")
	}
	for _, path := range []string{"/api/providers/test", "/api/providers/models"} {
		requestJSON(t, host, http.MethodPost, path, "{}", 501)
	}
	requestJSON(t, host, http.MethodPost, "/api/providers/cc-switch/import", "{}", 400)
}
