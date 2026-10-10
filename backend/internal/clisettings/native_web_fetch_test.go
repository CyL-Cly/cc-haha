package clisettings

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeWebFetchPreflightSettingsMatchActualSourceSchema(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("requires explicit pinned Bun for original SettingsSchema")
	}
	scenarios := []string{`{}`, `{"skipWebFetchPreflight":true}`, `{"skipWebFetchPreflight":false,"future":{"keep":true}}`, `{"skipWebFetchPreflight":null}`, `{"skipWebFetchPreflight":"true"}`, `{"skipWebFetchPreflight":3,"model":7}`, `{"skipWebFetchPreflight":{},"webSearch":{"mode":"invalid"}}`, `{"future":{"keep":true},"webSearch":{"mode":"auto"},"skipWebFetchPreflight":true,"model":"sonnet"}`}
	input, _ := json.Marshal(map[string]any{"schemas": scenarios})
	allocation := t.TempDir()
	home := filepath.Join(allocation, "source-home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-settings.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "NODE_ENV=production", "CC_HAHA_NATIVE_PLUGIN_SETTINGS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Source schema: %v %.4000s", err, output)
	}
	var source struct {
		Schemas []struct {
			Settings *string
			Errors   string
		}
	}
	if err := json.Unmarshal(output, &source); err != nil {
		t.Fatal(string(output), err)
	}
	if len(source.Schemas) != len(scenarios) {
		t.Fatal("Source scenario count", len(source.Schemas))
	}
	for i, input := range scenarios {
		got, err := ValidateNativeJSONContent(input, "fixture")
		if err != nil {
			t.Fatalf("scenario%d %s: %v", i, input, err)
		}
		want := source.Schemas[i]
		if (got.Settings == nil) != (want.Settings == nil) {
			t.Fatalf("scenario%d Settings mismatch: Go%v Source%v", i, got.Settings, want.Settings)
		}
		if want.Settings != nil {
			raw, _ := got.Settings.MarshalJSON()
			if string(protocol.StringifyValidatedJavaScriptJSON(raw)) != string(protocol.StringifyValidatedJavaScriptJSON([]byte(*want.Settings))) {
				t.Fatalf("scenario%d Go%s Source%s", i, raw, *want.Settings)
			}
		}
		raw, _ := json.Marshal(got.Errors)
		if string(protocol.StringifyValidatedJavaScriptJSON(raw)) != string(protocol.StringifyValidatedJavaScriptJSON([]byte(want.Errors))) {
			t.Fatalf("scenario%d errors Go%s Source%s", i, raw, want.Errors)
		}
	}
	if _, err := ValidateJSONContent(`{"skipWebFetchPreflight":true}`, "finite"); err == nil {
		t.Fatal("finite execution guard widened")
	}
}
