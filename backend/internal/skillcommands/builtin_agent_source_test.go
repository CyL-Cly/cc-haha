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
)

func TestBuiltinAgentOriginalDeclarationParserPromptAndErrors(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	arguments := []string{"", " ", "Explore", "Explore ", " Explore  inspect files ", "general-purpose\nread file\nthen return\n", "alpha\tbeta\tmore", "a\u00a0b", "\ufeffa b\ufeff", "a\u0085b", "a\u0085 b", "\u200ba b", "a b\u200b", "a\v\fc", `"my agent" do task`, `general-purpose echo '$ARGUMENTS'`, "中文 完整保留😀", "😀 😀中文", "a\u2028b", "a\u2029b", "a\u1680b", "a\u2007b", "a\u3000b"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("testdata/builtin_agent.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BUILTIN_AGENT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production"}
	input, _ := json.Marshal(map[string]any{"home": home, "arguments": arguments})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Metadata map[string]any
		Rows     []struct {
			Parsed *AgentCommandArgs
			Prompt []struct{ Type, Text string }
			Error  string
		}
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(arguments) {
		t.Fatal("invalid Source rows", err, string(out))
	}
	definition, err := NativeAgentCommand()
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(definition.Metadata())
	var actualMetadata map[string]any
	_ = json.Unmarshal(metadata, &actualMetadata)
	if !reflect.DeepEqual(actualMetadata, source.Metadata) {
		t.Fatal("actual declaration changed", string(metadata), source.Metadata)
	}
	for index, args := range arguments {
		parsed := ParseAgentCommandArgs(args)
		if !reflect.DeepEqual(parsed, source.Rows[index].Parsed) {
			t.Errorf("parse%d %q Go%v Source%v", index, args, parsed, source.Rows[index].Parsed)
		}
		prompt, failure := definition.Prompt(ctx, &args, Invocation{})
		want := source.Rows[index]
		if want.Error != "" {
			if failure == nil || failure.Error() != want.Error {
				t.Errorf("error%d Go%v Source%s", index, failure, want.Error)
			}
		} else if failure != nil || len(want.Prompt) != 1 || want.Prompt[0].Type != "text" || prompt != want.Prompt[0].Text {
			t.Errorf("prompt%d Go%s/%v Source%v", index, prompt, failure, want.Prompt)
		}
	}
}
