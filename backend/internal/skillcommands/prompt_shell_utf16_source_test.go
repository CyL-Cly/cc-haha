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

func TestOriginalPromptShellParserKeepsUTF16CommandAndPatternIdentity(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	texts := []json.RawMessage{json.RawMessage("\"before\\ud800 !`printf 'left\\ud800right'` after\\udc00\""), json.RawMessage("\"```!\\n printf '中文🙂\\udc00' \\n```\\ud800\""), json.RawMessage("\"```!\\n echo !`nested\\ud800` \\n``` !`next\\udc00`\"")}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "shellMatches": texts})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		ShellMatches []struct{ Commands []json.RawMessage }
		Capabilities struct{ Blocked []json.RawMessage }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.ShellMatches) != len(texts) || len(source.Capabilities.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	for index, text := range texts {
		matches, err := PromptShellMatchesJSON(text)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != len(source.ShellMatches[index].Commands) {
			t.Fatal(index, len(matches), string(output))
		}
		result := text
		for at, match := range matches {
			got, _ := protocol.DecodeJavaScriptJSONString(match.CommandJSON)
			want, _ := protocol.DecodeJavaScriptJSONString(source.ShellMatches[index].Commands[at])
			if !reflect.DeepEqual(got, want) {
				t.Fatal("Source command identity lost", index, at, string(match.CommandJSON), string(source.ShellMatches[index].Commands[at]))
			}
			result, err = ReplacePromptShellMatchJSON(result, match, "$& literal")
			if err != nil {
				t.Fatal(err)
			}
		}
		if index == 0 {
			want := json.RawMessage(`"before\ud800 $& literal after\udc00"`)
			gotUnits, _ := protocol.DecodeJavaScriptJSONString(result)
			wantUnits, _ := protocol.DecodeJavaScriptJSONString(want)
			if !reflect.DeepEqual(gotUnits, wantUnits) {
				t.Fatal("actual first replacement lost surrounding units", string(result))
			}
		}
	}
}
