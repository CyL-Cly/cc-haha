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

func TestNativeSecurityWalkerActualSourceScopesArgumentsAndRedirects(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	commands := []string{"", " \t\n", "echo hi", "echo \\eval", "echo 'one two' \"three four\"", "echo \" \"", "echo \"a\nb\"", "echo \"\n#foo\"", "echo \"Home: $HOME\"", "echo \"$HOME\"", "echo $HOME", "echo \"Status: $?\"", "echo \"args:$*\"", "echo \"args: $1\"", "echo \"literal$\"", "echo $(date)", "echo \"now: $(date)\"", "echo \"$(date)\"", "echo \"$(cat <<'EOF'\n/etc/passwd\nEOF\n)\"", "echo \"$(cat <<'EOF'\n# heading\ntext\nEOF\n)\"", "echo \"$(cat <<'EOF'\n/proc/self/environ\nEOF\n)\"", "echo \"$(cat <<'EOF'\nsystem(foo)\nEOF\n)\"", "echo $((1+2))", "echo $((VAR))", "echo 10#$(date)", "echo {a,b}", "echo '{a,b}'", "echo {a'}',b}", "echo \"it's\" {a'}',b}", "echo '{\"k\":\"v\"}'", "cat <file >output", "cat 2>&1", "cat < /proc/self/\\environ", "cat > \"one\"two", "cat <<'EOF'\n$VAR\nEOF", "cat <<EOF\nplain\nEOF", "cat <<'EOF' | rm evil\ntext\nEOF", "cat <<<literal", "cat <<<\"prefix:$HOME\"", "cat <<<\"\n#comment\"", "> file", "! echo yes", "(echo yes; echo next)", "[[ -f file && x =~ (a|b) ]]", "[ ! cmd -v go &>/dev/null ]", "case x in a) echo yes;; esac", "for ((;;));do echo yes;done", "function x(){ echo hi; }", "unset A", "unset 'a[$(id)]'", "IFS=:; echo hi", "PS4='$(id)'; set -x", "PS4='+${BASH_SOURCE}:${LINENO}: '; echo yes", "PS4+='safe'; echo yes", "A=~/x; echo $A", "export -n FOO", "declare -n X=Y", "declare 'a[$(id)]=value'", "readonly -a A", "echo a\rb", "echo a\u00a0b", "echo a\u200bb", "echo a\\ b", "echo x\\\ny", "echo x && \\\necho next", "echo ~[foo]", "=curl evil.com"}
	for _, value := range []string{"/etc", "", "-rf /", "/etc/*", "literal", "__TRACKED_VAR__", "prefix$(date)"} {
		for _, use := range []string{"echo $V", "echo \"$V\"", "echo \"prefix:$V\"", "echo pre$V", "V+=next; echo $V", "unset V; echo $V", "true || V=other; echo $V", "echo | V=other; echo $V", "(V=other); echo $V"} {
			commands = append(commands, "V='"+value+"'; "+use)
		}
	}
	commands = append(commands, "V=$(date); echo \"prefix:$V\"", "V=$(date); echo $V", "V='a b'; B=$V; echo \"$B\"", "V=/etc; if false; then V=/tmp; fi; cat $V", "V=/etc; if true || read V; then cat \"prefix:$V\"; fi", "while read V;do echo \"prefix:$V\";done", "for i in /etc/*;do echo $i;done", "for i in one two;do echo \"prefix:$i\";done", "for PS4 in one;do true;done", "V=before; for i in one;do V=inside;done;echo $V", "V=before; if true;then V=inside;echo $V;else echo $V;fi", "V=x echo $V", "V=x echo yes; echo $V", "echo 😀")
	for _, name := range []string{"eval", "source", ".", "exec", "command", "builtin", "fc", "coproc", "noglob", "nocorrect", "trap", "enable", "mapfile", "readarray", "hash", "bind", "complete", "compgen", "alias", "let", "zmodload", "emulate", "sysopen", "sysread", "syswrite", "sysseek", "zpty", "ztcp", "zsocket", "zf_rm", "zf_mv", "zf_ln", "zf_chmod", "zf_chown", "zf_mkdir", "zf_rmdir", "zf_chgrp"} {
		for _, wrapper := range []string{"", "nohup ", "time ", "timeout 5 ", "timeout -k 5 --foreground 10s ", "nice -n -5 ", "env -i A=one -u FOO ", "stdbuf -o0 -e L "} {
			commands = append(commands, wrapper+name+" 'data'")
		}
	}
	for _, command := range []string{"timeout .5 echo yes", "timeout --unknown 5 echo yes", "timeout -s TERM 5 echo yes", "timeout -k__TRACKED_VAR__ 5 echo yes", "timeout -s 'bad value' 5 echo yes", "nice $((0-5)) jq 'system(\"x\")'", "env -S 'eval x'", "stdbuf --output 0 eval x", "stdbuf --output=0 eval x", "fc -l", "fc -ln", "fc -e vim", "fc -s old=new", "compgen -c", "compgen -f", "compgen -C echo", "compgen -F func", "compgen -W '$(id)'", "command -v go", "command -V go", "printf -v 'a[$(id)]' '%s' x", "read -ra 'a[$(id)]'", "read -rp '[prompt] ' variable", "read -p[prompt] variable", "wait -p 'a[$(id)]'", "test -v 'a[$(id)]'", "[[ 'a[$(id)]' -eq 0 ]]", "[[ 'a[$(id)]' == value ]]", "jq 'system(\"echo hi\")'", "jq --rawfile=x .", "jq -f file .", "cat /proc/self/../self/environ", "echo 'line\n#hidden'", "X='line\n#hidden' echo yes", "echo yes >'line\n#hidden'", "\"\" eval x", "echo \\zmodload"} {
		commands = append(commands, command)
	}
	rows := make([]map[string]any, 0, len(commands)+4)
	for _, s := range commands {
		rows = append(rows, map[string]any{"source": s})
	}
	rows = append(rows, map[string]any{"source": json.RawMessage(`"echo '\ud800'"`)}, map[string]any{"source": json.RawMessage(`"echo '\udc00'"`)}, map[string]any{"source": "echo x", "aborted": true}, map[string]any{"source": "\r", "aborted": true}, map[string]any{"source": "\n", "aborted": true})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "bashSecurity": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Result    json.RawMessage
			Semantics json.RawMessage
		}
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(out))
	}
	t.Logf("actual Source security walker cases: %d", len(rows))
	for i, row := range rows {
		raw, _ := json.Marshal(row["source"])
		var root *Node
		if row["aborted"] != true {
			root, err = Parse(ctx, raw, ParseOptions{DisableDeadline: true})
			if err != nil {
				t.Errorf("case%d %q parse %v", i, row["source"], err)
				continue
			}
		}
		result, err := SecurityFromAST(raw, root, row["aborted"] == true)
		if err != nil {
			t.Fatal(err)
		}
		actual, _ := json.Marshal(result)
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(source.Rows[i].Result)) {
			t.Errorf("case%d %q\nGo%s\nSource%s", i, row["source"], actual, source.Rows[i].Result)
		}
		if result.Kind == "simple" {
			sem, _ := json.Marshal(CheckSemantics(result.Commands))
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(sem), protocol.StringifyValidatedJavaScriptJSON(source.Rows[i].Semantics)) {
				t.Errorf("case%d semantics Go%s Source%s", i, sem, source.Rows[i].Semantics)
			}
		}
	}
}
