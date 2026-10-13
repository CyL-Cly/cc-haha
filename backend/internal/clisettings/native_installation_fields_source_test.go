package clisettings

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeInstallationChannelAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	rows := []string{`{}`, `{"autoUpdatesChannel":"stable","future":{"keep":true}}`, `{"autoUpdatesChannel":"latest","autoMemoryEnabled":false}`, `{"autoUpdatesChannel":"unknown"}`, `{"autoUpdatesChannel":null}`, `{"autoUpdatesChannel":false}`, `{"autoUpdatesChannel":1}`, `{"autoUpdatesChannel":[]}`, `{"autoUpdatesChannel":{},"model":7}`, `{"autoUpdatesChannel":"STABLE","defaultShell":false}`, `{"minimumVersion":"1.2.3","autoUpdatesChannel":"stable","future":true}`, `{"minimumVersion":""}`, `{"minimumVersion":"bad"}`, `{"minimumVersion":null}`, `{"minimumVersion":false}`, `{"minimumVersion":7}`, `{"minimumVersion":{}}`}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-settings.fixture.ts")
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	_ = os.MkdirAll(home, 0700)
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "NODE_ENV=production", "CC_HAHA_NATIVE_PLUGIN_SETTINGS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"schemas": rows})
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Schemas []struct {
			Settings *string
			Errors   string
		}
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Schemas) != len(rows) {
		t.Fatal(err, string(out))
	}
	for i, row := range rows {
		parsed, err := ValidateNativeJSONContent(row, "fixture")
		if err != nil {
			t.Fatal(i, err)
		}
		want := source.Schemas[i]
		if (parsed.Settings == nil) != (want.Settings == nil) {
			t.Fatal(i, parsed, want)
		}
		if want.Settings != nil {
			raw, _ := parsed.Settings.MarshalJSON()
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(raw), protocol.StringifyValidatedJavaScriptJSON([]byte(*want.Settings))) {
				t.Fatal(i, string(raw), *want.Settings)
			}
		}
		errors, _ := json.Marshal(parsed.Errors)
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(errors), protocol.StringifyValidatedJavaScriptJSON([]byte(want.Errors))) {
			t.Fatal(i, string(errors), want.Errors)
		}
	}
	if _, err := ValidateJSONContent(`{"autoUpdatesChannel":"stable"}`, "finite"); err == nil {
		t.Fatal("finite schema expanded")
	}
}
