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

func TestNativeWebSearchMetadataSettingsMatchActualSourceSchema(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("requires explicit pinned Bun for original SettingsSchema")
	}
	values := []string{`{}`, `null`, `true`, `4`, `"string"`, `[]`, `{"unknown":true}`, `{"mode":null}`, `{"mode":"AUTO"}`, `{"mode":4}`, `{"tavilyApiKey":null}`, `{"tavilyApiKey":7,"braveApiKey":false}`, `{"tavilyApiKey":"\ud800","braveApiKey":" secret "}`, `{"mode":"brave","tavilyApiKey":"t","braveApiKey":"b","future":"strip"}`}
	for _, mode := range []string{"auto", "anthropic", "tavily", "brave", "disabled"} {
		raw, _ := json.Marshal(mode)
		values = append(values, `{"mode":`+string(raw)+`}`)
	}
	scenarios := []string{`{"future":{"keep":true}}`}
	for _, value := range values {
		scenarios = append(scenarios, `{"webSearch":`+value+`,"model":"sonnet","future":{"keep":true}}`)
	}
	scenarios = append(scenarios, `{"webSearch":{"mode":"invalid","tavilyApiKey":7},"model":7,"env":{"X":null}}`)
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
	if _, err := ValidateJSONContent(`{"webSearch":{}}`, "finite"); err == nil {
		t.Fatal("finite execution guard widened")
	}
}
