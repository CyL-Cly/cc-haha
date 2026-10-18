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

func TestNativeConsumerSettingsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	rows := []string{
		`{}`, `{"claudeMdExcludes":[],"plansDirectory":"","spinnerVerbs":{"mode":"append","verbs":[]}}`,
		`{"claudeMdExcludes":["**/CLAUDE.md","line\ud800",""],"plansDirectory":"../outside","spinnerVerbs":{"mode":"replace","verbs":["验证中",""],"future":true},"future":{"preserve":7}}`,
		`{"plansDirectory":null}`, `{"plansDirectory":false}`, `{"plansDirectory":7}`, `{"plansDirectory":[]}`, `{"plansDirectory":{}}`,
		`{"claudeMdExcludes":null}`, `{"claudeMdExcludes":"**"}`, `{"claudeMdExcludes":{}}`, `{"claudeMdExcludes":[null,1,false,{},[]]}`,
		`{"spinnerVerbs":null}`, `{"spinnerVerbs":false}`, `{"spinnerVerbs":"replace"}`, `{"spinnerVerbs":[]}`, `{"spinnerVerbs":{}}`,
		`{"spinnerVerbs":{"mode":"APPEND","verbs":[]}}`, `{"spinnerVerbs":{"mode":null,"verbs":null}}`, `{"spinnerVerbs":{"mode":false,"verbs":[1,null]}}`,
		`{"spinnerVerbs":{"mode":"append"}}`, `{"spinnerVerbs":{"verbs":["one"]}}`, `{"spinnerVerbs":{"mode":"replace","verbs":[false,{},[]]}}`,
		`{"spinnerVerbs":{"mode":"append","verbs":["A\ud800","B"]},"plansDirectory":"path\ud800","claudeMdExcludes":["**/日记/**"]}`,
		`{"plansDirectory":false,"spinnerVerbs":{"mode":"bad","verbs":[1]},"claudeMdExcludes":[false],"autoUpdatesChannel":"unknown","autoMemoryEnabled":false}`,
		`{"showThinkingSummaries":true}`, `{"showThinkingSummaries":false}`, `{"showThinkingSummaries":null}`, `{"showThinkingSummaries":"true"}`, `{"showThinkingSummaries":1}`, `{"showThinkingSummaries":[]}`, `{"showThinkingSummaries":{}}`,
		`{"autoDreamEnabled":true}`, `{"autoDreamEnabled":false}`, `{"autoDreamEnabled":null}`, `{"autoDreamEnabled":"true"}`, `{"autoDreamEnabled":1}`, `{"autoDreamEnabled":[]}`, `{"autoDreamEnabled":{}}`,
		`{"companyAnnouncements":[]}`, `{"companyAnnouncements":["实际团队消息","","lone\ud800"]}`, `{"companyAnnouncements":null}`, `{"companyAnnouncements":"message"}`, `{"companyAnnouncements":false}`, `{"companyAnnouncements":{}}`, `{"companyAnnouncements":[1,null,{},[],false]}`,
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-settings.fixture.ts")
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
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
	for _, key := range []string{"claudeMdExcludes", "plansDirectory", "spinnerVerbs", "showThinkingSummaries", "autoDreamEnabled", "companyAnnouncements"} {
		if _, err := ValidateJSONContent(`{"`+key+`":null}`, "finite"); err == nil {
			t.Fatal("finite schema expanded", key)
		}
	}
}
