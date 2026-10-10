package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestNativePluginOptionsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	f := newNativePluginFixture(t)
	if err := os.MkdirAll(f.n.Temp, 0700); err != nil {
		t.Fatal(err)
	}
	settings := `{"future":{"keep":true},"pluginConfigs":{"sample@mkt":{"options":{"token":"legacy-plaintext","count":2,"old":"keep"}},"other@mkt":{"options":{"outside":"keep"}}}}`
	credentials := `{"claudeAiOauth":{"accessToken":"owned-fixture"},"futureSecure":true,"pluginSecrets":{"sample@mkt":{"count":"old-sensitive","oldSecret":"keep"},"sample@mkt/server":{"token":"server-owned"},"other@mkt":{"outside":"secret-keep"}}}`
	f.write(t, filepath.Join(f.config, "settings.json"), settings)
	f.write(t, filepath.Join(f.config, ".credentials.json"), credentials)
	// Project scope is the same private HOME for both actual settings graphs.
	f.n.CWD = f.home
	p, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	secure, err := securestorage.New(securestorage.Options{Home: f.home, ConfigDir: f.config, Platform: "darwin", ConfigDirExplicit: true, Environment: f.n.Environment, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }})
	if err != nil {
		t.Fatal(err)
	}
	options, err := p.PluginOptions(secure)
	if err != nil {
		t.Fatal(err)
	}
	parse := func(s string) protocol.JSONObject {
		var o protocol.JSONObject
		if err := o.UnmarshalJSON([]byte(s)); err != nil {
			t.Fatal(err)
		}
		return o
	}
	operations := []map[string]any{
		{"action": "load", "id": "sample@mkt"},
		{"action": "save", "id": "sample@mkt", "values": parse(`{"token":"new-sensitive","count":7,"flag":true}`), "schema": parse(`{"token":{"sensitive":true},"count":{"sensitive":false}}`)},
		{"action": "load", "id": "sample@mkt"},
		{"action": "save", "id": "sample@mkt", "values": parse(`{"token":23,"tags":["first","second"],"count":"secret count"}`), "schema": parse(`{"count":{"sensitive":true}}`)},
		{"action": "load", "id": "sample@mkt"},
		{"action": "delete", "id": "sample@mkt"},
		{"action": "load", "id": "sample@mkt"},
		{"action": "load", "id": "other@mkt"},
	}
	observations := []protocol.JSONObject{}
	for _, step := range operations {
		id := step["id"].(string)
		switch step["action"] {
		case "load":
			value, err := options.Load(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			observations = append(observations, value)
		case "save":
			if err := options.Save(context.Background(), id, step["values"].(protocol.JSONObject), step["schema"].(protocol.JSONObject)); err != nil {
				t.Fatal(err)
			}
		case "delete":
			if err := options.Delete(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		}
	}
	actualSettings, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	actualCredentials, err := os.ReadFile(filepath.Join(f.config, ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-options.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_PLUGIN_OPTIONS_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"settings": settings, "credentials": credentials, "operations": operations})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	expected, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source plugin options: %v %s", err, stderr.String())
	}
	data, _ := json.Marshal(map[string]any{"observations": observations, "settings": string(actualSettings), "credentials": string(actualCredentials)})
	var a, b any
	_ = json.Unmarshal(data, &a)
	if err := json.Unmarshal(expected, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Go=%s Source=%s", data, expected)
	}
}
