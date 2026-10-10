package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"os"
	"path/filepath"
	"testing"
)

func TestCcSwitchHTTPRescansBeforeAtomicImportAndNeverExposesScanCredentials(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	options.Store = storage.New(storage.Options{})
	_, host := testHost(t, options)
	dir := filepath.Join(options.HomeDir, ".cc-switch")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "config.json")
	old := []byte(`{"claude":{"current":"one","providers":{"one":{"name":"Imported provider","settingsConfig":{"env":{"ANTHROPIC_BASE_URL":"https://private.invalid","ANTHROPIC_AUTH_TOKEN":"fake-old-private-key","ANTHROPIC_MODEL":"model[1m]"}}},"no-key":{"name":"Skipped","settingsConfig":{"env":{"ANTHROPIC_BASE_URL":"https://private.invalid","ANTHROPIC_MODEL":"model"}}},"lone\ud800":{"name":"UTF16 provider","settingsConfig":{"env":{"ANTHROPIC_BASE_URL":"https://other.invalid","ANTHROPIC_AUTH_TOKEN":"fake-other-private-key","ANTHROPIC_MODEL":"m\ud800[1m]"}}}}}}`)
	if e := os.WriteFile(path, old, 0600); e != nil {
		t.Fatal(e)
	}
	scan := requestJSON(t, host, "GET", "/api/providers/cc-switch/scan", "", 200)
	if bytes.Contains(scan, []byte("fake-old-private-key")) || bytes.Contains(scan, []byte("fake-other-private-key")) {
		t.Fatal("scan leaked credential")
	}
	var view struct {
		Available  bool
		Candidates []protocol.JSONObject
	}
	if json.Unmarshal(scan, &view) != nil || !view.Available || len(view.Candidates) != 3 {
		t.Fatal(string(scan))
	}
	// A key rotated after the scan must be resolved again on the server. The
	// browser never supplies credentials or trusts a stale candidate object.
	next := bytes.ReplaceAll(old, []byte("fake-old-private-key"), []byte("fake-rotated-private-key"))
	if e := os.WriteFile(path, next, 0600); e != nil {
		t.Fatal(e)
	}
	imported := requestJSON(t, host, "POST", "/api/providers/cc-switch/import", `{"sourceIds":["claude:one","claude:one","unknown","claude:no-key","claude:lone\ud800"],"apiKey":"forged-client-key"}`, 200)
	var result struct {
		Imported []protocol.JSONObject
		Skipped  []protocol.JSONObject
	}
	if e := json.Unmarshal(imported, &result); e != nil {
		t.Fatal(e)
	}
	if len(result.Imported) != 2 || len(result.Skipped) != 2 || result.Imported[0].String("apiKey") != "fake-rotated-private-key" || result.Skipped[0].String("reason") != "not-found" || result.Skipped[1].String("reason") != "no-api-key" {
		t.Fatal(string(imported))
	}
	var models protocol.JSONObject
	_ = json.Unmarshal(result.Imported[1].Get("models"), &models)
	u, _ := protocol.DecodeJavaScriptJSONString(models.Get("main"))
	if len(u) != 2 || u[1] != 0xd800 {
		t.Fatal("import lost original UTF16 model")
	}
	persisted, e := os.ReadFile(filepath.Join(options.ConfigDir, "cc-haha", "providers.json"))
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(persisted, []byte("fake-rotated-private-key")) || bytes.Contains(persisted, []byte("forged-client-key")) {
		t.Fatal("atomic import did not persist actual rescanned values")
	}
	sourceBytes, _ := os.ReadFile(path)
	if !bytes.Equal(sourceBytes, next) {
		t.Fatal("import mutated cc-switch source")
	}
	listing := requestJSON(t, host, "GET", "/api/providers", "", 200)
	var list protocol.JSONObject
	_ = json.Unmarshal(listing, &list)
	// Source GET exposes the sorted providers and activeId; providerOrder is
	// persisted metadata and is returned separately by the reorder action.
	var saved protocol.JSONObject
	if e = json.Unmarshal(persisted, &saved); e != nil {
		t.Fatal(e)
	}
	var order []string
	_ = json.Unmarshal(saved.Get("providerOrder"), &order)
	if len(order) != 5 || order[0] != result.Imported[0].String("id") || order[1] != result.Imported[1].String("id") {
		t.Fatal("import persisted display order dropped", string(persisted))
	}
	var displayed []protocol.JSONObject
	if e = json.Unmarshal(list.Get("providers"), &displayed); e != nil || len(displayed) != 2 || displayed[0].String("id") != order[0] || displayed[1].String("id") != order[1] || list.Get("providerOrder") != nil {
		t.Fatal("Source provider list display order or response shape changed", e, string(listing))
	}
	for _, body := range []string{`{}`, `{"sourceIds":[]}`, `{"sourceIds":[null]}`, `{"sourceIds":[1]}`, `{"sourceIds":[""]}`} {
		requestJSON(t, host, "POST", "/api/providers/cc-switch/import", body, 400)
	}
	requestJSON(t, host, "POST", "/api/providers/cc-switch/scan", "{}", 405)
	requestJSON(t, host, "GET", "/api/providers/cc-switch/import", "", 405)
	requestJSON(t, host, "GET", "/api/providers/cc-switch/unknown", "", 404)
}
