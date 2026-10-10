package providerconfig

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

var testContext = context.Background()

func fixture(t *testing.T) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	store := storage.New(storage.Options{})
	options := settings.Options{Store: store, HomeDir: home, ConfigDir: config, Environment: func() map[string]string { return map[string]string{"CLAUDE_CONFIG_DIR": config} }, EnsureMigrated: func(context.Context, string) error { return nil }, ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }, GitIgnore: func(context.Context, string, string) error { return nil }}
	user, err := settings.New(options)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := settings.NewManaged(options)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{Store: store, ConfigDir: config, Managed: managed, Settings: user})
	if err != nil {
		t.Fatal(err)
	}
	return service, config
}
func seed(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}
func saved(id string) storage.Object {
	return object(json.RawMessage(`{"id":"` + id + `","presetId":"deepseek","name":"Fixture DeepSeek","apiKey":"fake-private-key","baseUrl":"http://127.0.0.1:1/anthropic","apiFormat":"anthropic","models":{"main":"deepseek-v4-flash","haiku":"","sonnet":"deepseek-v4-flash","opus":"deepseek-v4-pro"},"futureProvider":{"enabled":true}}`))
}
func indexJSON(values ...storage.Object) string {
	return string(encode(storage.Object{"schemaVersion": encode(6), "providers": encode(values), "activeId": json.RawMessage("null")}))
}
func assertStatus(t *testing.T, err error, status int) {
	t.Helper()
	var api *settings.APIError
	if !errors.As(err, &api) || api.StatusCode != status {
		t.Fatalf("status=%d error=%v", status, err)
	}
}

func sameJSON(raw json.RawMessage, expected string) bool {
	var left, right any
	if json.Unmarshal(raw, &left) != nil || json.Unmarshal([]byte(expected), &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func TestLegacyProviderIndexNormalizesWithoutRewritingAndRetainsUnknowns(t *testing.T) {
	service, _ := fixture(t)
	value := saved("old")
	value["toolSearchEnabled"] = encode("auto:5")
	value["models"] = json.RawMessage(`{"main":" deepseek-v4-flash ","haiku":" ","sonnet":" s ","opus":" o ","fable":" f "}`)
	value["model1mSupport"] = json.RawMessage(`{"main":true,"haiku":false,"sonnet":false,"opus":false}`)
	value["supportsNestedToolResultMedia"] = encode("invalid-old-value")
	badValue := saved("invalid")
	badValue["imageGeneration"] = json.RawMessage(`{"model":6}`)
	original := string(encode(storage.Object{"providers": encode([]storage.Object{value, badValue}), "activeProviderId": encode("old"), "providerOrder": json.RawMessage(`["grok-official","old","unknown","old"]`), "officialProviderModels": json.RawMessage(`{"futureProvider":{"opaque":true},"claude-official":{"main":"x","haiku":"","sonnet":"","opus":""}}`), "futureRoot": json.RawMessage(`{"nested":[1,true]}`)}))
	seed(t, service.indexPath(), original)
	index, err := service.ReadIndex(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if str(index, "activeId") != "old" || string(index["schemaVersion"]) != "1" || len(providers(index)) != 1 {
		t.Fatal("legacy selection/filter changed")
	}
	provider := providers(index)[0]
	if string(provider["toolSearchEnabled"]) != "false" || str(object(provider["models"]), "haiku") != "deepseek-v4-flash" || string(object(provider["model1mSupport"])["fable"]) != "false" {
		t.Fatal("legacy normalization changed")
	}
	if _, exists := provider["supportsNestedToolResultMedia"]; exists {
		t.Fatal("invalid legacy media setting retained")
	}
	if _, exists := index["activeProviderId"]; exists {
		t.Fatal("consumed legacy key retained")
	}
	if string(provider["futureProvider"]) != `{"enabled":true}` || string(index["futureRoot"]) != `{"nested":[1,true]}` || string(object(index["officialProviderModels"])["futureProvider"]) != `{"opaque":true}` {
		t.Fatal("unknown data lost")
	}
	if !reflect.DeepEqual(order(index), []string{"grok-official", "old", "claude-official", "openai-official"}) {
		t.Fatalf("order=%v", order(index))
	}
	raw, _ := os.ReadFile(service.indexPath())
	if string(raw) != original {
		t.Fatal("read rewrote legacy file")
	}
}
func TestProviderCRUDReorderNullRemovalAndProtectedActiveDelete(t *testing.T) {
	service, _ := fixture(t)
	initial := saved("old")
	initial["requestCompatibility"] = json.RawMessage(`{"maxOutputTokens":4096,"future":{"keep":true}}`)
	seed(t, service.indexPath(), string(encode(storage.Object{"schemaVersion": encode(6), "activeId": json.RawMessage("null"), "providers": encode([]storage.Object{initial}), "providerOrder": encode([]string{"grok-official", "old", "claude-official", "openai-official"}), "futureRoot": json.RawMessage(`{"keep":true}`)})))
	input := saved("discarded-client-id")
	input["clientOnly"] = encode(true)
	added, err := service.Add(testContext, input)
	if err != nil {
		t.Fatal(err)
	}
	id := str(added, "id")
	if id == "discarded-client-id" || id == "" || len(added["futureProvider"]) > 0 || len(added["clientOnly"]) > 0 {
		t.Fatal("create did not apply Source schema")
	}
	index, _ := service.ReadIndex(testContext)
	if !reflect.DeepEqual(order(index), []string{"grok-official", "old", id, "claude-official", "openai-official"}) {
		t.Fatal("new provider inserted incorrectly")
	}
	updated, err := service.Update(testContext, "old", storage.Object{"name": encode("Renamed"), "requestCompatibility": json.RawMessage("null"), "model1mSupport": json.RawMessage("null")})
	if err != nil {
		t.Fatal(err)
	}
	if str(updated, "name") != "Renamed" || len(updated["requestCompatibility"]) != 0 || string(updated["futureProvider"]) != `{"enabled":true}` {
		t.Fatal("update lost future field or nullable deletion")
	}
	if _, err = service.Reorder(testContext, []string{id, "old"}); err != nil {
		t.Fatal(err)
	}
	index, _ = service.ReadIndex(testContext)
	if !reflect.DeepEqual(order(index), []string{"grok-official", id, "old", "claude-official", "openai-official"}) {
		t.Fatal("legacy reorder moved built-ins")
	}
	_, err = service.Reorder(testContext, []string{id, id})
	assertStatus(t, err, 400)
	if err = service.Activate(testContext, id); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, service.Delete(testContext, id), 409)
	if err = service.Delete(testContext, "old"); err != nil {
		t.Fatal(err)
	}
	index, _ = service.ReadIndex(testContext)
	if !sameJSON(index["futureRoot"], `{"keep":true}`) || len(providers(index)) != 1 {
		t.Fatal("CRUD lost root unknowns")
	}
}
func TestActivationSyncIsolatedManagedSettingsAndOfficialModelMapping(t *testing.T) {
	service, config := fixture(t)
	seed(t, service.indexPath(), indexJSON(saved("deep")))
	original := `{"env":{"ANTHROPIC_API_KEY":"fake-original-user-key","USER_ENV":"keep"},"futureUser":{"keep":true}}`
	seed(t, filepath.Join(config, "settings.json"), original)
	seed(t, filepath.Join(config, "cc-haha", "settings.json"), `{"env":{"ANTHROPIC_API_KEY":"stale-fixture","OPENAI_CODEX_OAUTH_FILE":"old-fixture","KEEP":"kept","futureEnv":{"keep":true}},"futureManaged":{"keep":true}}`)
	if err := service.Activate(testContext, "deep"); err != nil {
		t.Fatal(err)
	}
	current, err := service.options.Managed.ReadSettings(testContext)
	if err != nil {
		t.Fatal(err)
	}
	env := object(current["env"])
	if str(env, "ANTHROPIC_AUTH_TOKEN") != "fake-private-key" || str(env, "ANTHROPIC_API_KEY") != "" || str(env, "KEEP") != "kept" || len(env["OPENAI_CODEX_OAUTH_FILE"]) != 0 || !sameJSON(env["futureEnv"], `{"keep":true}`) {
		t.Fatal("managed sync changed auth or unknown env")
	}
	raw, _ := os.ReadFile(filepath.Join(config, "settings.json"))
	if string(raw) != original {
		t.Fatal("saved provider activation overwrote user settings")
	}
	if err := service.ActivateOfficial(testContext); err != nil {
		t.Fatal(err)
	}
	current, _ = service.options.Managed.ReadSettings(testContext)
	env = object(current["env"])
	if len(env["ANTHROPIC_MODEL"]) > 0 || str(env, "KEEP") != "kept" || !sameJSON(current["futureManaged"], `{"keep":true}`) {
		t.Fatal("official clear lost unknown managed state")
	}
	user, err := service.options.Settings.GetUserSettings(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if !sameJSON(user["futureUser"], `{"keep":true}`) || str(user, "model") != "claude-opus-5-5" || str(object(user["env"]), "ANTHROPIC_DEFAULT_FABLE_MODEL") != "claude-fable-5-1" || str(object(user["env"]), "USER_ENV") != "keep" {
		t.Fatal("official model mapping failed to preserve user state")
	}
}
func TestInvalidProviderPatchNeverMutatesExistingIndex(t *testing.T) {
	service, _ := fixture(t)
	original := indexJSON(saved("old"))
	seed(t, service.indexPath(), original)
	for _, raw := range []string{`{"name":null}`, `{"autoCompactWindow":15999}`, `{"autoCompactWindow":1.5}`, `{"modelContextWindows":{"m":10000001}}`, `{"model1mSupport":{"main":true}}`, `{"imageGeneration":{"model":" "}}`, `{"requestCompatibility":{"maxOutputTokens":9007199254740992}}`, `{"requestCompatibility":{"sampling":"guess"}}`, `{"toolSearchEnabled":"auto"}`, `{"models":{"main":"m","haiku":"h","sonnet":"s","opus":"o","fable":5}}`} {
		_, err := service.Update(testContext, "old", object(json.RawMessage(raw)))
		assertStatus(t, err, 400)
		current, _ := os.ReadFile(service.indexPath())
		if string(current) != original {
			t.Fatalf("invalid patch mutated file: %s", raw)
		}
	}
}
func TestRuntimeSelectionUsesActualLegacyIndexAndPreservesUnmanagedEnvironment(t *testing.T) {
	service, _ := fixture(t)
	seed(t, service.indexPath(), string(encode(storage.Object{"activeProviderId": encode("old"), "providers": encode([]storage.Object{saved("old")})})))
	base := map[string]string{"ANTHROPIC_API_KEY": "stale", "ANTHROPIC_MODEL": "stale", "CC_HAHA_OPENAI_OAUTH_PROVIDER": "1", "API_TIMEOUT_MS": "stale-preset-default", "KEEP": "value"}
	env, err := ResolveRuntimeEnv(testContext, service.options.Store, service.options.ConfigDir, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "fake-private-key" || env["ANTHROPIC_MODEL"] != "deepseek-v4-flash" || env["ENABLE_TOOL_SEARCH"] != "false" || env["KEEP"] != "value" || env["CC_HAHA_OPENAI_OAUTH_PROVIDER"] != "" || env["API_TIMEOUT_MS"] != "" {
		t.Fatal("selected env projection changed")
	}
	if base["ANTHROPIC_MODEL"] != "stale" {
		t.Fatal("runtime resolution mutated caller env")
	}
	official, err := ResolveRuntimeEnv(testContext, service.options.Store, service.options.ConfigDir, base, &Selection{ModelID: "claude-fixture", EffortLevel: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if official["ANTHROPIC_API_KEY"] != "" || official["ANTHROPIC_MODEL"] != "claude-fixture" || official["CLAUDE_CODE_ATTRIBUTION_HEADER"] != "1" || official["KEEP"] != "value" {
		t.Fatal("explicit official selection failed")
	}
}
