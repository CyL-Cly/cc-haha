package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/commandnames"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestInsightsFirstMeaningfulPromptMatchesOriginalFullBuiltinMembershipAndUTF16(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	user := func(content json.RawMessage) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user")}, protocol.JSONField{Name: "message", Value: raw(map[string]json.RawMessage{"content": content})})
	}
	sets := [][]protocol.JSONObject{{}, {user(raw("real\n human"))}, {user(raw("<command-name>/settings</command-name><command-args>x</command-args>")), user(raw("meaningful"))}, {user(raw("<command-name>/custom</command-name><command-args>\ufeff args \u3000</command-args>"))}, {user(raw("<command-name>/custom</command-name>")), user(raw("after empty custom"))}, {user(raw([]map[string]string{{"type": "text", "text": "<ide_opened_file>metadata</ide_opened_file>"}, {"type": "text", "text": "actual VS Code text"}}))}, {user(raw("<bash-input>echo &amp; verify</bash-input>"))}, {user(raw("[Request interrupted by user]")), user(raw("after interruption"))}, {user(raw("[request interrupted by user] lowercase"))}, {user(raw(strings.Repeat("a", 199) + "😀 rest"))}, {user(raw("<COMMAND-NAME attr='x'>/custom</COMMAND-NAME><command-args>task</command-args>"))}, {user(raw("<command-name><command-name>/custom</command-name></command-name><command-args>arg</command-args>"))}, {user(json.RawMessage(`"real\ud800text"`))}}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../goalstate/testdata/goal-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GOAL_COMMAND_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NODE_ENV=production"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "firstPrompts": sets}))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var source struct {
		Values []struct {
			Value json.RawMessage
			Error any
		}
	}
	if err = json.Unmarshal(data, &source); err != nil {
		t.Fatal(err, string(data))
	}
	builtins, err := commandnames.Set(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	for i, set := range sets {
		actual, err := FirstPrompt(set, builtins)
		if err != nil {
			if source.Values[i].Error == nil {
				t.Error(i, err)
			}
			continue
		}
		a, _ := protocol.DecodeJavaScriptJSONString(actual)
		b, _ := protocol.DecodeJavaScriptJSONString(source.Values[i].Value)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s Source%s", i, actual, source.Values[i].Value)
		}
	}
}
