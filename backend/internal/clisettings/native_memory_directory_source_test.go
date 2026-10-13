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

func TestNativeMemoryDirectorySchemaRetainsOriginalStringUnknownFieldsAndInvalidErrors(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	rows := []string{`{}`, `{"autoMemoryDirectory":"~/trusted","future":7}`, `{"autoMemoryDirectory":""}`, `{"autoMemoryDirectory":"/"}`, `{"autoMemoryDirectory":"../relative"}`, `{"autoMemoryDirectory":null}`, `{"autoMemoryDirectory":7}`, `{"autoMemoryDirectory":false}`, `{"autoMemoryDirectory":[]}`, `{"autoMemoryDirectory":{},"model":7}`, `{"autoMemoryDirectory":"e\u0301/\ud800","effortLevel":"high","future":{"keep":true}}`}
	rows = append(rows, `{"autoMemoryEnabled":true}`, `{"autoMemoryEnabled":false,"autoMemoryDirectory":"~/saved","future":"keep"}`, `{"autoMemoryEnabled":null}`, `{"autoMemoryEnabled":7}`, `{"autoMemoryEnabled":"false"}`, `{"autoMemoryEnabled":[],"model":7}`, `{"autoMemoryEnabled":{}}`)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-settings.fixture.ts")
	allocation := t.TempDir()
	home := filepath.Join(allocation, "source-home")
	_ = os.MkdirAll(home, 0700)
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "NODE_ENV=production", "CC_HAHA_NATIVE_PLUGIN_SETTINGS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"schemas": rows})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, out)
	}
	var source struct {
		Schemas []struct {
			Settings *string
			Errors   string
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Schemas) != len(rows) {
		t.Fatal("Source shape", e, string(out))
	}
	for i, row := range rows {
		parsed, e := ValidateNativeJSONContent(row, "fixture")
		if e != nil {
			t.Fatal(i, e)
		}
		want := source.Schemas[i]
		if (parsed.Settings == nil) != (want.Settings == nil) {
			t.Fatal(i, "settings shape", parsed, want)
		}
		if want.Settings != nil {
			raw, _ := parsed.Settings.MarshalJSON()
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(raw), protocol.StringifyValidatedJavaScriptJSON([]byte(*want.Settings))) {
				t.Fatalf("row%d settings %s Source%s", i, raw, *want.Settings)
			}
		}
		errors, _ := json.Marshal(parsed.Errors)
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(errors), protocol.StringifyValidatedJavaScriptJSON([]byte(want.Errors))) {
			t.Fatalf("row%d errors %s Source%s", i, errors, want.Errors)
		}
	}
	if _, e := ValidateJSONContent(`{"autoMemoryDirectory":"~/trusted"}`, "finite"); e == nil {
		t.Fatal("finite profile expanded")
	}
	if _, e := ValidateJSONContent(`{"autoMemoryEnabled":false}`, "finite"); e == nil {
		t.Fatal("finite memory gate expanded")
	}
}
