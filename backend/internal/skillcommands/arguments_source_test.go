package skillcommands

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestNativeSkillArgumentsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	type scenario struct {
		Content string   `json:"content"`
		Args    *string  `json:"args,omitempty"`
		Names   []string `json:"names"`
	}
	cases := []scenario{{Content: "$ARGUMENTS $0", Names: []string{}}}
	for _, args := range []string{
		"", "a b", `a "hello world" 'two words'`, `all'one'"token"`,
		`one\ two three`, `"a\"b" 'a\b'`, `*.go '?' "*"`,
		`one && two || three;four | five`, `one # ignored two`, `one#comment two`,
		`$HOME ${HOME} $$ $@x $?z`, `"$HOME/${PATH}" '$HOME'`,
		`$(command) <(other) >file`, `a ${} b`, `a ${broken`,
		`a "unterminated string`, "中文 😀\t文件", `'' "" empty`,
		`"$&" "$` + "`" + `" "$'" "$$"`,
	} {
		for _, content := range []string{"Use $first / $second / $firstX / $first[0]", "$ARGUMENTS[0]|$ARGUMENTS[1]|$0|$1|$9|$10x|$ARGUMENTS", "unchanged"} {
			copy := args
			cases = append(cases, scenario{content, &copy, []string{"first", "second"}})
		}
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-argument-substitution.fixture.ts")
	input, _ := json.Marshal(cases)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir, cmd.Stdin = home, bytes.NewReader(input)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_SKILL_ARGUMENT_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source Skill arguments: %v %s", err, stderr.Bytes())
	}
	var source []struct {
		Arguments []string
		Content   string
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source) != len(cases) {
		t.Fatalf("missing Source arguments: %v %s", err, output)
	}
	for i, row := range cases {
		t.Run(string(inputCase(row)), func(t *testing.T) {
			args := ""
			if row.Args != nil {
				args = *row.Args
			}
			content, err := SubstituteArguments(row.Content, row.Args, row.Names)
			if err != nil || !reflect.DeepEqual(ParseArguments(args), source[i].Arguments) || content != source[i].Content {
				t.Fatalf("Source argument parsing/substitution differs: Go %q %q err=%v; Source %q %q", ParseArguments(args), content, err, source[i].Arguments, source[i].Content)
			}
		})
	}
}

func inputCase(value any) []byte { data, _ := json.Marshal(value); return data }
