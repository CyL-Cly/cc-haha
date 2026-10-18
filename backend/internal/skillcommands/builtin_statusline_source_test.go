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

func TestStatuslineWholeOriginalDeclarationAndPrompt(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source runtime required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"", " ", "\ufeff\u00a0\u3000", "  keep colors  ", "Use \"quotes\", $ARGUMENTS, and $(literal)", "中文 😀\nkeep this line\n", "\u0085not JS whitespace\u0085", "\u200bkeep\u200b", "\u2028trim\u2029"}
	fixture, err := filepath.Abs("testdata/builtin_statusline.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_STATUSLINE_PROMPT_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production"}
	input, _ := json.Marshal(map[string]any{"home": home, "arguments": arguments})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Metadata protocol.JSONObject
		Rows     [][]struct{ Type, Text string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(arguments) {
		t.Fatal("invalid whole Source statusline result", err, string(out))
	}
	definition, err := NativeStatuslineCommand()
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected map[string]any
	metadata, _ := json.Marshal(definition.Metadata())
	want, _ := json.Marshal(source.Metadata)
	_ = json.Unmarshal(metadata, &actual)
	_ = json.Unmarshal(want, &expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("statusline declaration changed", string(metadata), string(want))
	}
	for index, args := range arguments {
		text, err := definition.Prompt(ctx, &args, Invocation{})
		if err != nil || len(source.Rows[index]) != 1 || source.Rows[index][0].Type != "text" || source.Rows[index][0].Text != text {
			t.Errorf("statusline profile%d changed: %v %q %+v", index, err, text, source.Rows[index])
		}
	}
}
