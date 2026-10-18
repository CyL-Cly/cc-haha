package pluginmcp

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

func TestActualSourceSavedPluginOptionUTF16ReachesMCPConfiguration(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	s, home, environment := newFixture(t, `{"future":{"keep":true}}`, `{}`)
	plugin := parse(t, `{"name":"unicode","source":"unicode@mkt","repository":"unicode@mkt","enabled":true,"manifest":{"userConfig":{"region":{"type":"string"}}}}`)
	plugin.Set("path", raw(filepath.Join(home, "installed")))
	data, err := s.DataDirectory(plugin.String("source"))
	if err != nil {
		t.Fatal(err)
	}
	environment["CC_HAHA_INTERPOLATION_VALUE"] = "env 中文🙂"
	rows := []map[string]any{}
	actual := []protocol.JSONObject{}
	for _, value := range []string{`"a\ud800b"`, `"a\udc00b"`, `"中文🙂\ud800\u0000"`, `["left\ud800",null,"right\udc00"]`, `-0`, `1e400`, `true`, `null`, `{"nested":"\ud800"}`} {
		values := parse(t, `{"region":`+value+`}`)
		if value[0] == '"' {
			if err := s.options.PluginOptions.Save(context.Background(), plugin.String("source"), values, parse(t, `{"region":{"type":"string"}}`)); err != nil {
				t.Fatal(err)
			}
			values, err = s.options.PluginOptions.Load(context.Background(), plugin.String("source"))
			if err != nil {
				t.Fatal(err)
			}
		}
		for _, shape := range []string{
			`{"command":"run-${user_config.region}","args":["before\udc00","${user_config.region}","${CC_HAHA_INTERPOLATION_VALUE}","${CC_HAHA_INTERPOLATION_MISSING:-fallback}"],"env":{"CONFIG":"${user_config.region}","RAW":"left\ud800right"},"future":{"keep":"\ud800"}}`,
			`{"type":"http","url":"https://owned.invalid/${user_config.region}","headers":{"X-Config":"${user_config.region}","X-Raw":"left\udc00right"},"future":{"keep":"\ud800"}}`,
		} {
			configuration := parse(t, shape)
			for _, key := range []string{"env", "headers"} {
				if fields, ok := configuration.Object(key); ok {
					fields.Set("INHERITED", raw("${user_config.constructor}::${user_config.toString}::${user_config.__proto__}"))
					configuration.Set(key, raw(fields))
				}
			}
			var failures []protocol.JSONObject
			resolved, err := s.Resolve(context.Background(), configuration, plugin, &values, &failures, "unicode", "server")
			if err != nil || len(failures) != 0 {
				t.Fatal("actual resolved private config", err, failures)
			}
			rows = append(rows, map[string]any{"config": configuration, "values": values})
			actual = append(actual, resolved)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-option-interpolation.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_OPTION_INTERPOLATION=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"rows": rows, "plugin": plugin, "dataDirectory": data, "environment": map[string]string{"CC_HAHA_INTERPOLATION_VALUE": environment["CC_HAHA_INTERPOLATION_VALUE"]}})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal("actual Source option resolver", err, stderr.String())
	}
	var source struct {
		Results []struct {
			Value   protocol.JSONObject
			Failure *string
		}
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(actual) {
		t.Fatal("actual resolver receipts", err, len(source.Results), len(actual))
	}
	for index, value := range actual {
		want := source.Results[index]
		if want.Failure != nil {
			t.Fatal("Source resolver rejected valid input", index, *want.Failure)
		}
		// Decode values as UTF-16 at each actual structured leaf; ordinary Go
		// JSON string decoding would hide this regression by replacing units.
		var leaves func(protocol.JSONObject) []string
		leaves = func(object protocol.JSONObject) []string {
			result := []string{}
			for _, property := range object.OwnProperties() {
				name, _ := protocol.DecodeJavaScriptJSONString(property.NameJSON)
				result = append(result, string(protocol.QuoteJavaScriptUTF16(name)))
				if units, err := protocol.DecodeJavaScriptJSONString(property.Value); err == nil {
					result = append(result, string(protocol.QuoteJavaScriptUTF16(units)))
				} else if len(property.Value) > 0 && property.Value[0] == '{' {
					result = append(result, leaves(objectFromRaw(property.Value))...)
				} else if len(property.Value) > 0 && property.Value[0] == '[' {
					var items []json.RawMessage
					_ = json.Unmarshal(property.Value, &items)
					for _, item := range items {
						units, err := protocol.DecodeJavaScriptJSONString(item)
						if err != nil {
							t.Fatal(err)
						}
						result = append(result, string(protocol.QuoteJavaScriptUTF16(units)))
					}
				} else {
					result = append(result, string(property.Value))
				}
			}
			return result
		}
		if !reflect.DeepEqual(leaves(value), leaves(want.Value)) {
			t.Errorf("actual Source resolver UTF16/config row%d differs\nGo:%q\nSource:%q", index, leaves(value), leaves(want.Value))
		}
	}
}

func objectFromRaw(value json.RawMessage) protocol.JSONObject {
	var result protocol.JSONObject
	_ = result.UnmarshalJSON(value)
	return result
}
