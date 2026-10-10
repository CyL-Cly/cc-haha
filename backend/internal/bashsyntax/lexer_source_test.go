package bashsyntax

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

func TestNativeLexerActualSourceUTF16OffsetsOperatorsQuotesAndContinuations(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	type row struct {
		Source   json.RawMessage `json:"source"`
		Position string          `json:"position"`
	}
	rows := []row{}
	commands := []string{`""`, `"   \t\r"`, `"echo hi && git status | tee log"`, `"NODE_ENV=x echo one 2>&1 <input >>out"`, `"\n #comment\n echo hi; ;; ;;& ;&"`, `"[[ a == b ]] && [ -f c ]"`, `"{ echo yes; }"`, `"! echo no"`, `"'one two' \"three four\" $'a\\n'"`, `"$(echo hi) ${VAR:-x} $(( 1 + 2 )) ` + "`echo x`" + `"`, `"<(sort a) >(cat) &>all &>>more >|- 3<&- 2>&- <<<one <<-EOF\nbody\nEOF"`, `"echo one\\\ntwo\\\r\nthree \\ next\\ arg\\\tvalue"`, `"trailing\\"`, `"echo -12 34 +56 -abc 1>file"`, `"echo \u4f60\u597d \ud83d\ude00 x\ud800z x\udfffz"`, `"\ud800"`, `"\udfff"`, `"a\u00a0b\u2028c\u0000d\u000be"`}
	for _, op := range []string{"&&", "||", "|&", ";;&", ";;", ";&", ">>", ">&-", ">&", ">|", "&>>", "&>", "<<<", "<<-", "<<", "<&-", "<&", "<(", ">(", "((", "))", "|", "&", ";", ">", "<", "(", ")", "[[", "[", "{", "}", "!"} {
		raw, _ := json.Marshal("word " + op + " arg")
		commands = append(commands, string(raw))
	}
	for _, source := range commands {
		for _, position := range []string{"cmd", "arg"} {
			rows = append(rows, row{json.RawMessage(source), position})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "bashLexer": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	type observation struct {
		Tokens    []token `json:"tokens"`
		I         int     `json:"i"`
		B         int     `json:"b"`
		Bytes     int     `json:"bytes"`
		ByteTable []int   `json:"byteTable"`
	}
	var source struct{ Rows []observation }
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(out))
	}
	for i, r := range rows {
		lexer, err := newLexer(r.Source)
		if err != nil {
			t.Fatal(err)
		}
		actual := observation{Tokens: []token{}}
		for j := 0; j < 100000; j++ {
			token := lexer.next(r.Position)
			actual.Tokens = append(actual.Tokens, token)
			if token.Type == "EOF" {
				break
			}
		}
		actual.I, actual.B = lexer.i, lexer.b
		actual.ByteTable = make([]int, len(lexer.src)+1)
		for j := range actual.ByteTable {
			actual.ByteTable[j] = lexer.byteAt(j)
		}
		actual.Bytes = actual.ByteTable[len(lexer.src)]
		want := source.Rows[i]
		for j := range actual.Tokens {
			actual.Tokens[j].Value = protocol.StringifyValidatedJavaScriptJSON(actual.Tokens[j].Value)
		}
		if !reflect.DeepEqual(actual, want) {
			a, _ := json.Marshal(actual)
			b, _ := json.Marshal(want)
			t.Errorf("case%d %s %s Go%s Source%s", i, r.Position, r.Source, a, b)
		}
	}
}
