package skillcommands

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPromptMentionsAndRangesMatchOriginalProductionRegexes(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	inputs := []string{
		"ordinary email@example.test and literal x@y",
		"@file.txt @file.txt @other.md#heading",
		`@"my file.txt" @unquoted @"my file.txt"`,
		"punctuation (@file.txt) @other.txt, @end.md!",
		"@one#L10 @two#L2-5 @three#L8-3 @zero#L0-0 @four#fragment#twice",
		`@"code-reviewer (agent)" @agent-code-reviewer @agent-plugin:scope.name @"plugin:scope.name (agent)"`,
		"@server:resource/path @server:https://example.test/data @server:resource/path",
		"@a\u00a0@b\ufeff@c\u2028@d\r\n@e",
		"@你好 @é @emoji😀 @node_1 @a-b",
		`@"regular (agent) trailing" @"not an agent! (agent)" @agent-汉字 @"a@b (agent)"`,
		"@file#L9007199254740993-9007199254740995 @file#L2#heading @file##double @#empty",
		"@quoted:thing @\"quoted:other\" inline@agent-a.\n@agent-a..",
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "mentions": inputs})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.2400s", e, output)
	}
	var source struct {
		Mentions     []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Mentions) != len(inputs) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v rows%d blocked%v %.1800s", e, len(source.Mentions), source.Capabilities.Blocked, output)
	}
	for i, text := range inputs {
		got, e := ExtractPromptMentions(text)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := json.Marshal(got)
		if e != nil {
			t.Fatal(e)
		}
		if string(protocol.StringifyValidatedJavaScriptJSON(raw)) != string(protocol.StringifyValidatedJavaScriptJSON(source.Mentions[i])) {
			t.Errorf("input%d Go=%s Source=%s", i, raw, source.Mentions[i])
		}
	}
}
