package clibootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestPluginOptionsUseActualSharedSettingsAndCredentialFiles(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"future":{"keep":true},"pluginConfigs":{"sample@mkt":{"options":{"token":"legacy-plaintext","count":2,"old":"keep"}},"other@mkt":{"options":{"outside":"keep"}}}}`)
	f.write(t, filepath.Join(f.config, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"owned-only-fixture"},"futureSecure":true,"pluginSecrets":{"sample@mkt":{"count":"old-sensitive","oldSecret":"keep"},"sample@mkt/server":{"token":"server-owned"},"other@mkt":{"outside":"secret-keep"}}}`)
	p, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	secure, err := securestorage.New(securestorage.Options{Home: f.home, ConfigDir: f.config, Platform: "linux", Environment: f.n.Environment})
	if err != nil {
		t.Fatal(err)
	}
	options, err := p.PluginOptions(secure)
	if err != nil {
		t.Fatal(err)
	}
	value, err := options.Load(context.Background(), "sample@mkt")
	if err != nil || value.String("count") != "old-sensitive" || value.String("token") != "legacy-plaintext" {
		t.Fatal(value, err)
	}
	parse := func(s string) protocol.JSONObject {
		t.Helper()
		var value protocol.JSONObject
		if err := value.UnmarshalJSON([]byte(s)); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if err := options.Save(context.Background(), "sample@mkt", parse(`{"token":"new-sensitive","count":7,"flag":true}`), parse(`{"token":{"sensitive":true},"count":{"sensitive":false}}`)); err != nil {
		t.Fatal(err)
	}
	settings, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(settings), "new-sensitive") || strings.Contains(string(settings), "legacy-plaintext") {
		t.Fatal("sensitive value remained in settings")
	}
	var disk map[string]json.RawMessage
	_ = json.Unmarshal(settings, &disk)
	if string(disk["future"]) != `{"keep":true}` && !strings.Contains(string(disk["future"]), `"keep": true`) {
		t.Fatal("unknown settings field lost", string(settings))
	}
	value, err = options.Load(context.Background(), "sample@mkt")
	if err != nil || value.String("token") != "new-sensitive" || string(value.Get("count")) != "7" || value.String("old") != "keep" || value.String("oldSecret") != "keep" {
		t.Fatal(value, err)
	}
	secrets, err := secure.Read(context.Background())
	if err != nil || secrets.Get("claudeAiOauth") == nil || secrets.Get("futureSecure") == nil {
		t.Fatal(secrets, err)
	}
	var all protocol.JSONObject
	_ = all.UnmarshalJSON(secrets.Get("pluginSecrets"))
	var current protocol.JSONObject
	_ = current.UnmarshalJSON(all.Get("sample@mkt"))
	if current.Get("count") != nil {
		t.Fatal("non-sensitive move left secure copy", current)
	}
	if err := options.Delete(context.Background(), "sample@mkt"); err != nil {
		t.Fatal(err)
	}
	secrets, _ = secure.Read(context.Background())
	_ = all.UnmarshalJSON(secrets.Get("pluginSecrets"))
	if all.Get("sample@mkt") != nil || all.Get("sample@mkt/server") != nil || all.Get("other@mkt") == nil || secrets.Get("claudeAiOauth") == nil {
		t.Fatal("delete touched unrelated accounts or retained owned secrets", secrets)
	}
	settings, _ = os.ReadFile(filepath.Join(f.config, "settings.json"))
	if strings.Contains(string(settings), `"sample@mkt"`) || !strings.Contains(string(settings), `"other@mkt"`) {
		t.Fatal("delete scope", string(settings))
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PluginOptions(secure); err == nil {
		t.Fatal("closed process admitted a new credential consumer")
	}
}
