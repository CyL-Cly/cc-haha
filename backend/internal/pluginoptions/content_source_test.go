package pluginoptions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestContentSafeOptionsMatchOriginalMissingSensitivePrototypeAndCoercionBranches(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	parse := func(text string) protocol.JSONObject {
		var value protocol.JSONObject
		if err := value.UnmarshalJSON([]byte(text)); err != nil {
			t.Fatal(err)
		}
		return value
	}
	type example struct {
		Content        string `json:"content"`
		Values, Schema protocol.JSONObject
	}
	rows := []example{
		{"${user_config.missing}:${user_config.}", parse(`{}`), parse(`{}`)},
		{"${user_config.constructor}:${user_config.__proto__}", parse(`{}`), parse(`{}`)},
		{"${user_config.region}", parse(`{"region":"raw\ud800value"}`), parse(`{"region":{"sensitive":"true"}}`)},
		{"${user_config.region}", parse(`{"region":{"toString":null}}`), parse(`{"region":{"sensitive":true}}`)},
		{"${user_config.region}", parse(`{"region":{"toString":null}}`), parse(`{}`)},
		{"", parse(`{"孤\ud800":"must-hide"}`), parse(`{"孤\ud800":{"sensitive":true}}`)},
		{"${user_config.region}", parse(`{"region":"${user_config.secret}"}`), parse(`{"secret":{"sensitive":true}}`)},
	}
	// The source document contains a real lone UTF-16 template key, not the
	// six-character backslash spelling accepted by an ordinary Go string.
	contents := make([]json.RawMessage, len(rows))
	for i, row := range rows {
		contents[i], _ = json.Marshal(row.Content)
	}
	contents[5] = json.RawMessage(`"${user_config.孤\ud800}"`)
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-option-interpolation.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_OPTION_INTERPOLATION=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	inputs := []map[string]any{}
	for i, row := range rows {
		inputs = append(inputs, map[string]any{"content": contents[i], "values": row.Values, "schema": row.Schema})
	}
	data, _ := json.Marshal(map[string]any{"rows": inputs, "plugin": map[string]string{"path": filepath.Join(home, "plugin"), "source": "sample@mkt"}, "dataDirectory": filepath.Join(home, "data")})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Results []struct {
			Value   json.RawMessage
			Failure *string
		}
	}
	if err := json.Unmarshal(out, &source); err != nil || len(source.Results) != len(rows) {
		t.Fatal(err, string(out))
	}
	for i, row := range rows {
		got, err := SubstituteUserConfigInContentJSON(contents[i], row.Values, row.Schema)
		want := source.Results[i]
		if want.Failure != nil {
			if err == nil || err.Error() != *want.Failure {
				t.Errorf("row%d coercion error %v vs %s", i, err, *want.Failure)
			}
			continue
		}
		if err != nil {
			t.Fatal(i, err)
		}
		a, _ := protocol.DecodeJavaScriptJSONString(got)
		b, _ := protocol.DecodeJavaScriptJSONString(want.Value)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("row%d Go %s Source %s", i, got, want.Value)
		}
	}
}
