package sessionrewind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceRecordedBashClassification(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source classification requires pinned Bun 1.3.14")
	}
	_, err := NewRecordedCommandClassifier(runtime.GOOS, "external")
	if err != nil {
		t.Fatal(err)
	}
	commands := []string{
		"", " ", "cat <<'EOF'\ntext\nEOF", "cat <<EOF\n$(evil)\nEOF", "echo '<<EOF'", "jq '<<EOF'", "jq \"$(cat <<'EOF'\nhello\nEOF\n)\"", "echo \"$(cat <<'EOF'\nhello\nEOF\n)\"", "cat <<-\\EOF\n\tbody\n\tEOF", "cat <<'\\EOF'\nbody\n\\EOF", "cat <<EOF | wc -l\nbody\nEOF", "cat <<EOF <<SECOND\nbody\nEOF\nsecond\nSECOND", "cat \\<\\<EOF", "echo '$IFS' <<'EOF'\nbody\nEOF", "git status", "git status --short", "git diff --stat", "git diff --output=out", "git ls-remote origin", "git ls-remote https://github.com/a/b", "git reflog expire", "git remote show -n origin", "git remote add x y", "git tag --list 'v1'", "git tag v1", "git branch --merged main", "git branch main", "git branch --list '*topic*'", "cd elsewhere && git status", "git status && cat file", "echo hi | wc -l", "printf 'x'", "echo '中文😀'", "echo '$HOME'", "echo \"$HOME\"", "echo '$(evil)'", "echo 'a\nb'", "echo hi 2>&1", "echo hi >/dev/null", "cat </dev/null", "cat >HEAD", "echo hi ; touch HEAD ; git status",
		"node -v", "node -v --run task", "python --version", "python3 --version", "ls *", "ls '*'", "find . -name '*.go'", "find . -exec evil '{}' ';'", "rg -A20 pattern file", "rg --pre evil file", "rg '$VAR' file", "grep -n x file", "git diff '$Z--output=x'", "ps axe", "ps -e", "base64 -- --output=x", "date +%Y", "date 01011200", "date --date=yesterday +%Y", "tput clear", "tput colors", "lsof +mfile", "pyright --createstub os", "pyright -- --createstub os", "pyright file.py", "sort -nr", "sort -o out", "xargs -rI echo sh -c id", "xargs -E= EOF echo x", "xargs -E EOF echo x", "xargs grep x", "xargs sed x", "sed -n '1,3p' file", "sed 's/a/b/g'", "sed 's/a/b/g' file", "sed -i 's/a/b/' file", "sed -n '1p;2p' file", "sed 's/a/b/w out'", "sed 's/a/b/e'", "sed 's/a/b/;p'", "sed 's/a/b/3g'", "jq '.name' file", "jq 'system(\"id\")'", "jq --rawfile a file '.name'", "jq '.a | .b'", "echo a\\ b", "echo a\\;b", "echo a\x00b", "echo a\u00a0b", "echo a#b", "echo \"a#b\"", "echo hi # comment", "echo hi # 'quote'", "echo 'line\n#hidden'", "git diff --stat\r", "echo \"x\ry\"", "echo {a,b}", "echo '{a,b}'", "git diff --sort=x", "git tag --list -- --fake", "git status --porcelain=v1", "git log -10", "git remote -v", "git reflog show", "gh pr list", "unknown program", "pwd", "whoami", "history 3", "alias", "arch -h", "ifconfig en0", "ip addr", "uniq --skip-chars=0", "uniq input output", "git status \\\n&& cat file", "echo '$IFS'", "cat /proc/123/environ", "echo '-flag'", "cut -d':' -f1 file", "git diff \"--stat\"", "git diff --'stat'", "echo '\\\\'", "echo '\\\\' 'next'",
	}
	commands = append(commands, "jq", "jq ''", "jq 'a; b'", "jq | git status", "git commit -m 'hello'", `git commit -m "中文😀" --author="Name <email>"`, `git commit -m '-flag'`, `git commit -m "$(evil)"`, `git commit -m 'hello' > hooks/pre-commit`, `git commit -m 'hello' && git status`, `git commit -m 'has \ backslash'`, "git commit -m 'line\n#text'", "zmodload zsh/system", "FOO=bar command builtin nocorrect zmodload", "fc -e editor", "fc -le", "fc -l", "builtin syswrite", "emulate -c 'evil'")
	for _, wrapper := range []string{"", "NO_COLOR=1 ", "PATH=x ", "CI=1 ", "timeout 5 ", "timeout -k 2 --signal=TERM -- 5 ", "timeout -k$(evil) 5 ", "time -- ", "nice ", "nice -n 2 -- ", "nohup -- ", "stdbuf -o0 "} {
		for _, cd := range []string{"cd other", "pushd other", "popd"} {
			commands = append(commands, wrapper+cd+" && "+wrapper+"'git' status")
		}
		commands = append(commands, wrapper+"git status", "touch -- hooks/x && "+wrapper+"git status", "echo x > HEAD && "+wrapper+"git status")
	}
	for _, base := range []string{"git status", "git diff", "git tag --list", "git branch --list", "rg", "grep", "echo", "ls", "find . -name", "jq", "sed -n", "cut"} {
		for _, arg := range []string{`--help`, `"--help"`, `'--help'`, `--'help'`, `-"h"`, `''-h`, `""'-h'`, `'-'"h"`, `'-'h`, `"-"h`, `'$HOME'`, `"$HOME"`, `'a; b'`, `"a; b"`, `'a|b'`, `"a|b"`, `'a&b'`, `"a&b"`, `'x\\'`, `'x\\' 'next'`, `'{'`, `'{a,b}'`, `{a,b}`, `{1..3}`, `'x#y'`, `x#y`, `*`, `'*'`, `-A١`, `--help=`, `-- --evil`, `'中文😀'`, "\"a\nb\"", "'a\nb'", "'a\n#b'", "'a\rb'", "\"a\rb\"", `< /dev/null`, `> /dev/null`, `2 > /dev/null 2>&1`, `2>&1`, `\;`, `\&`, `\\;`, `x\ y`, `'$(echo)'`, `"$(echo)"`, "\"$(cat <<'EOF'\nhello\nEOF\n)\""} {
			commands = append(commands, base+" "+arg)
		}
	}
	for _, first := range []string{"git status", "echo hello", "cat file", "ls -la", "pwd", "git commit -m 'hello'", "cat <<'EOF'\ntext\nEOF"} {
		for _, op := range []string{" && ", " || ", " | ", " ; ", " & ", "\n", " \\\n"} {
			commands = append(commands, first+op+"git status", first+op+"touch hooks/x")
		}
	}
	for _, policy := range readonlyCompile.policy.Commands {
		commands = append(commands, policy.Name)
		for flag, kind := range policy.SafeFlags {
			command := policy.Name + " " + flag
			switch kind {
			case "number":
				command += " 2"
			case "string":
				command += " value"
			case "char":
				command += " x"
			case "{}":
				command += " '{}'"
			case "EOF":
				command += " EOF"
			}
			commands = append(commands, command)
		}
	}
	for _, profile := range []struct{ platform, userType string }{{runtime.GOOS, "external"}, {"windows", "external"}, {runtime.GOOS, "ant"}} {
		t.Run(profile.platform+"-"+profile.userType, func(t *testing.T) {
			classifier, err := NewRecordedCommandClassifier(profile.platform, profile.userType)
			if err != nil {
				t.Fatal(err)
			}
			profileCommands := append([]string{}, commands...)
			if profile.userType == "ant" {
				for _, policy := range readonlyCompile.policy.Ant {
					profileCommands = append(profileCommands, policy.Name, policy.Name+" --help", policy.Name+" a/b", policy.Name+" a/b/c", policy.Name+" https://example.invalid", policy.Name+" user@host")
				}
			}
			profileCommands = append(profileCommands, `cat \\host\file`, `cat //host/file`, `cat https://host/file`, `cat /\\host/file`, `cat \\/host/file`, `cat \\127.0.0.1\file`, `cat \\[::1]\file`, `cat DavWWWRoot`, `cat HOST@SSL@123`, `xargs echo hello`, `jq '$(cat <<EOF)'`, `jq "$(cat <<'EOF'\n{\"literal\":\"$(evil)\"}\nEOF\n)"`, `echo '<<EOF\n中文😀'`)
			root := t.TempDir()
			home := filepath.Join(root, "home")
			for _, leaf := range []string{"", "tmp", "config", "cache"} {
				if err = os.MkdirAll(filepath.Join(home, leaf), 0700); err != nil {
					t.Fatal(err)
				}
			}
			input := rewindFixture{Home: home, Messages: []protocol.JSONObject{}, Rows: []json.RawMessage{}, Complete: true, Files: []nativeFixtureFile{}, Commands: profileCommands, ClassifierPlatform: profile.platform, ClassifierUserType: profile.userType}
			raw, _ := json.Marshal(input)
			source, _ := filepath.Abs("testdata/source.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", source)
			cmd.Dir = home
			cmd.Stdin = bytes.NewReader(raw)
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "CC_HAHA_REWIND_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "SHELL=/bin/zsh", "USER_TYPE=external", "ANTHROPIC_API_KEY=fixture", "TZ=UTC", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("Source failed %v\n%s", err, stderr.String())
			}
			if stderr.Len() > 0 {
				t.Fatalf("Source diagnostics %s", stderr.String())
			}
			var expected struct {
				Commands []struct {
					Command        string `json:"command"`
					ReadOnly       bool   `json:"readOnly"`
					SecurityPasses bool   `json:"securityPasses"`
				} `json:"commands"`
				Capabilities struct{ Blocked, SecureReads []any } `json:"capabilities"`
			}
			if err = json.Unmarshal(output, &expected); err != nil {
				t.Fatalf("Source output: %v %s", err, output)
			}
			if len(expected.Capabilities.Blocked) > 0 || len(expected.Capabilities.SecureReads) > 0 || len(expected.Commands) != len(profileCommands) {
				t.Fatal("Source boundary/command evidence changed")
			}
			failures := 0
			for _, source := range expected.Commands {
				security, securityErr := classifier.security(source.Command)
				if securityErr != nil || security != source.SecurityPasses {
					t.Errorf("security %q: Go %v/%v; Source %v", source.Command, security, securityErr, source.SecurityPasses)
				}
				actual, err := classifier.Classify(source.Command)
				if err != nil || actual != source.ReadOnly {
					t.Errorf("command %q: Go %v/%v; Source %v", source.Command, actual, err, source.ReadOnly)
					failures++
					if failures > 25 {
						break
					}
				}
			}
		})
	}

}
func TestRecordedClassifierPropagatesUnsupportedDomain(t *testing.T) {
	classifier, err := NewRecordedCommandClassifier(runtime.GOOS, "external")
	if err != nil {
		t.Fatal(err)
	}
	_, err = classifier.Classify(string([]byte{255}))
	var cap *UnsupportedRecordedCommand
	if !errors.As(err, &cap) {
		t.Fatalf("unsupported UTF-8 was disguised as %v", err)
	}
	input := rewindFixture{Home: t.TempDir(), Complete: true, Messages: []protocol.JSONObject{fixtureMessage("u", "user", "/fixture", "prompt", ""), fixtureMessage("tool", "tool_use", "/fixture", []any{fixtureTool("bash", "Bash", map[string]any{"command": "cat <<'EOF'\ntext\nEOF"})}, "")}}
	service := fixtureService(t, input)
	service.options.ClassifyRecordedCommand = func(string) (bool, error) { return classifier.Classify(string([]byte{255})) }
	if _, err = service.List(context.Background(), "fixture", false); !errors.As(err, &cap) {
		t.Fatalf("List swallowed classifier domain %v", err)
	}
}

// The compound splitter can produce exactly "jq". JavaScript substring(3)
// returns an empty string there; indexing the Go byte string must not panic.
func TestShortJqFragmentsAndConcurrentReplay(t *testing.T) {
	classifier, err := NewRecordedCommandClassifier(runtime.GOOS, "external")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		command  string
		readOnly bool
	}{{"jq", false}, {"jq ''", true}, {"jq | git status", false}, {"jq 'a; b'", true}, {"jq && touch HEAD", false}, {"git status", true}, {"git diff --output=x", false}}
	var wg sync.WaitGroup
	for repeat := 0; repeat < 8; repeat++ {
		for _, test := range cases {
			wg.Add(1)
			go func() {
				defer wg.Done()
				actual, err := classifier.Classify(test.command)
				if err != nil || actual != test.readOnly {
					t.Errorf("%q => %v/%v", test.command, actual, err)
				}
			}()
		}
	}
	wg.Wait()
}
