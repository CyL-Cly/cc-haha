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

func TestSyncSubagentLimitActualSourceSchemaOrderAndErrors(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("explicitly pinned Source Bun required")
	}
	home := t.TempDir()
	home, e := filepath.EvalSymlinks(home)
	if e != nil {
		t.Fatal(e)
	}
	rows := []map[string]string{}
	for _, key := range []string{"maxConcurrentSubagents"} {
		for _, value := range []string{"1", "2", "9007199254740991", "null", "0", "-1", "1.5", "9007199254740992", "true", "\"2\"", "[]", "{}"} {
			rows = append(rows, map[string]string{"name": key + value, "content": `{"future":true,"model":"opus","` + key + `":` + value + `}`})
		}
	}
	rows = append(rows, map[string]string{"name": "schema-order", "content": `{"future":true,"agentTeamsEnabled":false,"maxConcurrentSubagents":2,"model":"opus","effortLevel":"low"}`})
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "scenarios": rows})
	fixture, e := filepath.Abs("../../../scripts/quality-gate/go-parity/native-model-fields.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	profile, e := filepath.Abs("../../testdata/loopback-only.sb")
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_NATIVE_MODEL_FIELDS_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source struct {
		Observations []struct {
			Observation struct {
				SettingsSource *string
				ErrorsSource   string
			}
		}
		SourceOnly struct{ Capabilities struct{ Blocked []string } }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Observations) != len(rows) || len(source.SourceOnly.Capabilities.Blocked) != 0 {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		t.Run(row["name"], func(t *testing.T) {
			got, e := ValidateNativeJSONContent(row["content"], "fixture")
			if e != nil {
				t.Fatal(e)
			}
			want := source.Observations[i].Observation
			if want.SettingsSource == nil {
				if got.Settings != nil {
					t.Fatal("invalid Source field accepted")
				}
			} else {
				if got.Settings == nil {
					t.Fatal("valid Source field rejected", string(rawJSON(got.Errors)))
				}
				encoded, _ := got.Settings.MarshalJSON()
				if string(protocol.StringifyValidatedJavaScriptJSON(encoded)) != string(protocol.StringifyValidatedJavaScriptJSON([]byte(*want.SettingsSource))) {
					t.Fatal(string(encoded), *want.SettingsSource)
				}
			}
			if string(protocol.StringifyValidatedJavaScriptJSON(rawJSON(got.Errors))) != string(protocol.StringifyValidatedJavaScriptJSON([]byte(want.ErrorsSource))) {
				t.Fatal(string(rawJSON(got.Errors)), want.ErrorsSource)
			}
			if _, e = ValidateJSONContent(row["content"], "finite"); e == nil {
				t.Fatal("finite setting capability widened")
			}
		})
	}
}
