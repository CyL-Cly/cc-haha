package clisettings

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeTerminalShellSettingAgainstActualSourceSchema(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	cases := []string{`{}`, `{"defaultShell":"bash"}`, `{"defaultShell":"powershell"}`, `{"future":{"keep":true},"defaultShell":"powershell","model":"actual"}`, `{"defaultShell":null}`, `{"defaultShell":true}`, `{"defaultShell":3}`, `{"defaultShell":{}}`, `{"defaultShell":[]}`, `{"defaultShell":"other"}`, `{"defaultShell":"Bash"}`, `{"defaultShell":"\ud800"}`}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-settings.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_PLUGIN_SETTINGS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"schemas": cases})
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source settings %v %.4000s", e, out)
	}
	var source struct {
		Schemas []struct {
			Settings *string
			Errors   string
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Schemas) != len(cases) {
		t.Fatalf("Source shape %v %.4000s", e, out)
	}
	for i, content := range cases {
		parsed, e := ValidateNativeJSONContent(content, "fixture")
		if e != nil {
			t.Fatal(i, e)
		}
		if source.Schemas[i].Settings == nil {
			if parsed.Settings != nil {
				t.Error("invalid Source setting retained", i)
			}
		} else {
			if parsed.Settings == nil {
				t.Fatal(i, "valid Source shell rejected", string(rawJSON(parsed.Errors)))
			}
			got, _ := parsed.Settings.MarshalJSON()
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(got), protocol.StringifyValidatedJavaScriptJSON([]byte(*source.Schemas[i].Settings))) {
				t.Errorf("case%d fields/order/olddata Go%s Source%s", i, got, *source.Schemas[i].Settings)
			}
		}
		if got, want := protocol.StringifyValidatedJavaScriptJSON(rawJSON(parsed.Errors)), protocol.StringifyValidatedJavaScriptJSON([]byte(source.Schemas[i].Errors)); !bytes.Equal(got, want) {
			t.Errorf("case%d validation Go%s Source%s", i, got, want)
		}
	}
	if _, e := ValidateJSONContent(`{"defaultShell":"bash"}`, "finite"); e == nil {
		t.Fatal("finite profile broadened")
	}
}
