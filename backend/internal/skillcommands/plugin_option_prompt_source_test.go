package skillcommands

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

func TestOriginalPluginPromptMethodRetainsOptionsUntilModelJSON(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	pluginPath := filepath.Join(home, "plugin-$&-$$")
	data := filepath.Join(home, "data")
	parse := func(text string) protocol.JSONObject {
		var value protocol.JSONObject
		if err := value.UnmarshalJSON([]byte(text)); err != nil {
			t.Fatal(err)
		}
		return value
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows := []map[string]any{}
	actual := [][]protocol.JSONObject{}
	for _, item := range []string{`"a\ud800b"`, `"a\udc00b"`, `"中文🙂\ud800\u0000"`, `["a\ud800",null,"b\udc00"]`, `-0`, `1e400`, `true`, `null`, `{"nested":"\ud800"}`} {
		for _, skillMode := range []bool{false, true} {
			values := parse(`{"region":` + item + `,"secret":"must-not-reach-model"}`)
			schema := parse(`{"region":{"type":"string"},"secret":{"type":"string","sensitive":true}}`)
			directory := filepath.Join(pluginPath, "skill-$&-$$")
			content := "${user_config.region}\n${user_config.secret}\n${user_config.absent}\n${CLAUDE_PLUGIN_ROOT}\n${CLAUDE_PLUGIN_DATA}\n${CLAUDE_SKILL_DIR}\n${CLAUDE_SESSION_ID}"
			for _, inherited := range []string{"constructor", "toString", "toLocaleString", "valueOf", "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__", "__proto__"} {
				content += "\n${user_config." + inherited + "}"
			}
			definition := Definition{body: content, name: "unicode:actual", pluginPath: pluginPath, pluginSource: "unicode@mkt", pluginUserConfig: schema, pluginSkillMode: skillMode, shell: "bash"}
			if skillMode {
				definition.baseDir = directory
			}
			blocks, err := definition.PromptBlocks(ctx, nil, Invocation{SessionID: "session-$&-$$", Platform: "darwin", PluginOptions: func(context.Context, string) (protocol.JSONObject, error) { return values, nil }, PluginDataDirectory: func(context.Context, string) (string, error) {
				if err := os.MkdirAll(data, 0700); err != nil {
					return "", err
				}
				return data, nil
			}, ExecuteShellJSON: func(_ context.Context, request ShellRequest) (json.RawMessage, error) { return request.TextJSON, nil }})
			if err != nil {
				t.Fatal(err)
			}
			actual = append(actual, blocks)
			rows = append(rows, map[string]any{"prompt": true, "content": content, "values": values, "schema": schema, "directory": directory, "sessionID": "session-$&-$$", "skillMode": skillMode})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-option-interpolation.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_OPTION_INTERPOLATION=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"rows": rows, "plugin": map[string]string{"path": pluginPath, "source": "unicode@mkt"}, "dataDirectory": data})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal("original complete plugin prompt", err, stderr.String())
	}
	var source struct {
		Results []struct {
			Value   []protocol.JSONObject
			Failure *string
		}
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Results) != len(actual) {
		t.Fatal(err, string(output))
	}
	for index, got := range actual {
		want := source.Results[index]
		if want.Failure != nil || len(want.Value) != 1 || len(got) != 1 {
			t.Fatal(index, want.Failure, want.Value)
		}
		gotUnits, err := protocol.DecodeJavaScriptJSONString(got[0].Get("text"))
		if err != nil {
			t.Fatal(err)
		}
		wantUnits, err := protocol.DecodeJavaScriptJSONString(want.Value[0].Get("text"))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotUnits, wantUnits) {
			t.Errorf("whole original prompt row %d differs\nGo %s\nSource %s", index, got[0].Get("text"), want.Value[0].Get("text"))
		}
	}
}
