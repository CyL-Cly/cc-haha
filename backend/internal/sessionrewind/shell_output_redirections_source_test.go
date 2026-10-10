package sessionrewind

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualSourceSandboxRuleOutputRedirectionExtractionAndReconstruction(t *testing.T) {
	commands := []string{}
	for _, base := range []string{"echo text", `echo 'two words'`, `echo "x;y"`, `echo \(x\)`, `result=$(echo x)`, `echo $(echo x)tail`, `cat <(echo x)`, `(echo x)`, `# line\necho text`, `echo "$VAR"`, `echo a\nb`, `FOO=x timeout 10 command`, `echo ''`, `echo '> < |'`} {
		for _, suffix := range []string{"", " > out", " >> out", " 2> err", " 1> out", " 9> err", " 2>&1", " > /dev/null 2>&1", " >!out", " >! out", " >| out", " >>&! out", " >&| out", " >&! out", " >& out", " > $VAR", " 2>$HOME/file", " > ~name", " > %TEMP%", " 2>!~name", " > *.sh", " > '\"quoted path\"'", " > \\\npath", " > ''"} {
			commands = append(commands, base+suffix)
		}
	}
	commands = append(commands, "cat <<'EOF' > out\n${}\nEOF", "cat <<EOF\nhello\nEOF", "echo 'unterminated > out", `echo x >!\!foo`, `echo x >& 2`, `echo x 2>> err`, `(echo x) >> out`, `echo < < delimiter`)
	data, _ := json.Marshal(map[string]any{"commands": commands})
	fixture, _ := filepath.Abs("testdata/sandbox-redirections.fixture.ts")
	root, _ := filepath.Abs("../../..")
	home := t.TempDir()
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", filepath.Join(root, "preload.ts"), fixture)
	cmd.Env = []string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "DISABLE_TELEMETRY=1"}
	cmd.Stdin = bytes.NewReader(data)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []ShellOutputRedirections
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	classifier, e := NewRecordedCommandClassifier("darwin", "external")
	if e != nil {
		t.Fatal(e)
	}
	for i, command := range commands {
		actual, e := classifier.ExtractShellOutputRedirections(command)
		if e != nil || !reflect.DeepEqual(actual, source[i]) {
			t.Fatalf("actual Source%d command%q\nSource%+v\nGo%+v error%v", i, command, source[i], actual, e)
		}
	}
}
