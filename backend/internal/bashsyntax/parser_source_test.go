package bashsyntax

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

func TestNativeBashASTLiteralListsPipelinesGroupsAndStringsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	commands := []string{"", " \t\r\n", "echo hi", "123 -45", "! echo hi", "echo hi && git status || cat file", "cat a | grep b |& tee c", "\n#before\necho hi\n#after\n", "echo hi; printf yes & echo done", "( echo one; echo two )", "{ echo one; echo two; }", "( ( echo one ) && echo two )", "echo hi &&", "echo hi |", "echo hi\r\necho next", "echo 'one two' \"three four\"", "'command name' a", "pre'part'word\"and more\"", "echo \" \t\"", "echo \"first\nsecond\"", "echo \"unclosed", "echo 'unclosed", "echo [a-z]", "echo }foo", "echo 你好😀", "echo trailing\\", ";; echo after"}
	commands = append(commands, "A=1 echo yes", "A=1 B='two' command -x", "A=1", "A=1 B=2", "A+=more", "A=(one two 'three four')", "cat >out -x two", "2>&1 cat file", "<input cat >output", "echo hi <<< 'string'", "cat >x && echo bye >y", "cat | grep b 2>log | tee c", "! cat >out", "(cat) >out", "{ cat; } 2>err", ">out", "cat 2>&- 3<&-", "cat >> 'hello world'")
	commands = append(commands, "if true; then echo yes; fi", "if false; then echo first; elif true; then echo second; else echo third; fi", "while true; do echo hi; done", "until false; do echo hi; done", "for x in one two 'three four'; do echo hi; done", "select x in one two; do echo chosen; done", "function name() { echo yes; }", "name() { echo yes; } 2>log", "export FOO=one BAR='two words'", "declare -r VAR=value", "readonly VAR", "local -a value")
	commands = append(commands, "echo $VAR $_ $_more $? $$ $@ $1 $12", "echo pre$VAR'next'", "echo \" $VAR \"", "echo $(date) \"$(echo hi)\"", "echo $(< file)", "diff <(sort a) <(sort b)", "echo hi > >(cat)", "echo `date`", "echo \"`date`\"", "echo $'one\\ntwo'", "echo $\"translated\"", "echo bare$next$ \"bare$\"", "echo \"dollar$-x\"", "echo \"first\n$VAR\nthird\"", "echo \"foo\"`\n`\"bar\"", "X=$(date) echo $X")
	commands = append(commands, "echo ${VAR} ${#VAR} ${!VAR} ${!prefix*} ${VAR@U}", "echo ${VAR:-default} ${VAR:=one} ${VAR:+next} ${VAR?error}", "echo ${VAR:- } ${2+ ${2}}", "echo ${VAR:-(one two)}", "echo ${v//old/new} ${v/pat/$(echo hi)word}", "echo ${v#'one'*} ${v##\"quoted\"*} ${v%'str'*}", "echo ${v^^*} ${v,,*}", "echo \"${VAR:-\"${OTHER}\"}\"", "echo ${#!} ${!#} ${!## }", "echo ${v/\"${old}\"/new}", "echo ${v:-$(date)}", "echo ${v:-<(<file)}", "echo ${v:-one\n}")
	commands = append(commands, "echo $((1+2*3))", "echo $(( a=b=c=1, a++ ))", "(( ++i + a[1+2] ))", "echo $[2#101+0x1F]", "echo $(( condition ? 1 : other ? 2 : 3 ))", "echo $(( (1, 2) + -3 ** 2 ))", "echo ${var:1+2: -3}", "echo ${arr[++counter]} ${arr[@]} ${arr[((a+1))]}", "echo $(( a += b <<= 2 ))", "echo $(( a[0] * $VAR ))", "(( !x || y && z ))")
	commands = append(commands, "A[1+2]=value", "A[$var]+=more", "A[$?]=value", "A[$_]=value", "A[0]=(one two)", "A[x[y]]=value", "for ((i=0;i<3;i++)); do echo $i; done", "for ((;;)) { echo hi; }", "for ((i=0,j=1; i<3; ++i,j+=1))\ndo echo yes\ndone", "unset A -v 'a[$(id)]' \"$VAR\"", "unsetenv A B; echo yes", "if true; then echo yes; fi <<< str", "while false; do true; done <<<str", "case $var in a) echo yes;; b|c) echo next ;& *) echo no;;& esac", "case $var in (a) ;; -g) ;; -o) owner=$2;; esac", "case x in *\"foo\"*|pre'bar'post) echo yes;; esac", "case x in *(a|b)|*([0-9])([0-9])) echo yes;; esac", "case x in ${PN}.pot|*.[1357]) echo yes;; esac", "case x in bar\\ baz|one\\\n|two) echo yes;; esac")
	for _, base := range []string{"echo x", "printf '%s' value", "grep -n pattern file", "cat \"hello\""} {
		for _, op := range []string{"&&", "||", "|", "|&", ";", "&", "\n"} {
			commands = append(commands, base+" "+op+" "+"echo done")
		}
	}
	commands = append(commands, "[[ -f file ]]", "[ ! cmd -v go &>/dev/null ]", "[[ ! -f file || -z \"$value\" && ( -n other ) ]]", "[[ ! \"x\" =~ y ]]", "[[ \"$x\" =~ \"$y\" ]]", "[[ \"$x\" =~ ' boop '(.*)$ ]]", "[[ x =~ (a|b)[0-9]+ && -f file ]]", "[[ a == @(one|two) ]]", "[[ a != pre${var}'post'* ]]", "[[ a = *(a|b) ]]", "[ 1 -eq 2 -o 3 -lt 4 ]", "[[ 1 == 2 ]]", "[[ $x =~ [a-z ] ]]", "[[ $x == @(\"one\"|$VAR) ]]", "[[ a < b && c > d ]]", "[[ -f ]]", "[ ]", "[[ x || ]]", "[[ x && ]]", "[ one two ]")
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	commands = append(commands, "echo {1..5} {a..z} {1..a} {aa..zz}", "echo {one,two} {} {o[k]} {12,34}", "echo {;touch /tmp/evil", "echo {foo;rm x", "echo 10#${var} -0x12#$(date)", "cat <<EOF\nhello\nEOF\necho next", "cat <<'EOF'\n$VAR $(date)\nEOF", "cat <<\\EOF\n$VAR\nEOF", "cat <<-EOF\n\t$VAR text\n\tEOF", "cat 3<<END\nhello $VAR next $(date) tail\nEND", "cat <<EOF > out.txt\ntext\nEOF", "ls <<'EOF' | rm -rf /tmp/evil\ntext\nEOF", "cat <<EOF || echo fail\ntext\nEOF", "cat <<EOF; rm x\ntext\nEOF", "cat <<EOF - arg\nbody\nEOF", "cat <<EOF\nunterminated $VAR", "cat <<EOF\n\\$VAR \\` literal $'x'\nEOF", "cat <<EOF\n$((1+2))\nEOF")
	words := []string{"plain", "-123", "0x42", "'one two'", "\"quoted\"", "\"a\nb\"", "$VAR", "${v:-default}", "$(date)", "`date`", "$((1+2))", "$'a\\nb'", "{a,b}", "{1..3}", "[a-z]", "你好😀", "\\*", "\\$VAR"}
	for _, a := range words {
		for _, b := range words {
			commands = append(commands, "echo "+a+b, "echo "+a+" "+b)
		}
	}
	for _, op := range []string{">", ">>", "<", "<&", ">&", "&>", "&>>", ">|", "<&-", ">&-", "<<<", "2>", "3>>"} {
		for _, word := range words {
			commands = append(commands, "cat "+op+word, "if true;then echo yes;fi "+op+word)
		}
	}
	for _, word := range words {
		commands = append(commands, "name () { echo "+word+"; }", "function name { echo "+word+"; }", "declare -r A="+word, "A="+word+" B=other echo yes", "for x in "+word+";do echo yes;done", "case x in "+word+") echo yes;; esac")
	}
	for _, op := range []string{"=", "+=", "-=", "*=", "/=", "%=", "<<=", ">>=", "&=", "^=", "|=", "||", "&&", "|", "^", "&", "==", "!=", "<", ">", "<=", ">=", "<<", ">>", "+", "-", "*", "/", "%", "**"} {
		commands = append(commands, "echo $((1 "+op+" 2))", "for ((a=1; 1 "+op+" 2; a++));do echo yes;done")
	}
	for _, open := range []string{"'EOF'", "\"EOF\"", "EOF", "\\EOF", "EOF!"} {
		for _, body := range []string{"plain", "$VAR tail", "first $(date) second", "one `date` two", "\\$VAR \\` literal", "a\nb\n", "$((1+2))", "${v:-value}"} {
			end := "EOF"
			if open == "EOF!" {
				end = "EOF!"
			}
			commands = append(commands, "cat <<"+open+"\n"+body+"\n"+end+"\necho next")
		}
	}
	t.Logf("actual Source AST cases: %d", len(commands))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "bashSyntax": commands})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows []json.RawMessage }
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(commands) {
		t.Fatal(err, string(out))
	}
	for i, command := range commands {
		raw, _ := json.Marshal(command)
		node, err := Parse(ctx, raw, ParseOptions{DisableDeadline: true})
		if err != nil {
			t.Errorf("case%d %q %v", i, command, err)
			continue
		}
		actual := nodeDebug(node)
		want := string(protocol.StringifyValidatedJavaScriptJSON(source.Rows[i]))
		if actual != want {
			t.Errorf("case%d %q Go%s Source%s", i, command, actual, want)
		}
	}
}
