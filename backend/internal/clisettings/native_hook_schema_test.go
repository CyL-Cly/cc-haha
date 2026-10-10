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

func TestNativeHookSettingsMatchActualSourceSchema(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("requires explicit pinned Bun for original SettingsSchema")
	}
	scenarios := []string{`{}`, `{"hooks":{}}`, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo actual","future":true}],"future":true}]},"future":true}`, `{"hooks":{"Stop":[{"matcher":"custom","hooks":[{"type":"command","command":"\\ud800","shell":"bash","timeout":1,"once":true,"async":true,"asyncRewake":false,"statusMessage":"actual","if":"Read(*)"}]}]}}`, `{"hooks":null}`, `{"hooks":{"Unknown":[]}}`, `{"hooks":{"Stop":true}}`, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":1}]}]}}`, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"true","timeout":0}]}]}}`, `{"hooks":{"Stop":[{"hooks":[{"type":"prompt","prompt":"check","timeout":2,"model":"small"}]}]} }`, `{"hooks":{"Stop":[{"hooks":[{"type":"http","url":"https://example.com","headers":{"x":"value"},"allowedEnvVars":["X"]}]}]}}`, `{"hooks":{"Stop":[{"hooks":[{"type":"agent","prompt":"check"}]}]}}`}
	scenarios = append(scenarios, `{ "allowedHttpHookUrls":[],"httpHookAllowedEnvVars":[] }`, `{"httpHookAllowedEnvVars":["TOKEN","\ud800","TOKEN"],"allowedHttpHookUrls":["http://127.0.0.1/*","*"] ,"future":{"kept":true}}`, `{"allowedHttpHookUrls":null}`, `{"httpHookAllowedEnvVars":false}`, `{"httpHookAllowedEnvVars":[1,null,"okay"]}`, `{"allowedHttpHookUrls":[{},true]}`)
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
	if _, err := ValidateJSONContent(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"true"}]}]}}`, "finite"); err == nil {
		t.Fatal("finite execution guard widened")
	}
}
