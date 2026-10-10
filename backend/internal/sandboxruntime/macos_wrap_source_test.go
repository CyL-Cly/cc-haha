package sandboxruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActualSourceMacSandboxWrapperShellQuoteEnvironmentAndNoRestrictions(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	os.MkdirAll(cwd, 0700)
	commands := []string{"eval '/bin/sleep 30 & child=$!; printf \"%s\" \"$child\"; wait'", "eval '/bin/sleep 30 & jobs -p; wait'", "printf '%s' 'private bytes'", "echo \"$VALUE\" && echo '\\escaped'", "line1\nline2", strings.Repeat("🐙", 51), ""}
	rows := []map[string]any{}
	options := []MacWrapOptions{}
	inputs := []string{}
	for _, command := range commands {
		for _, ports := range [][2]float64{{0, 0}, {12345, 0}, {0, 1080}, {12345, 1080}} {
			h, s := ports[0], ports[1]
			read := &ReadRestriction{DenyOnly: []string{}, AllowWithinDeny: []string{}}
			write := &WriteRestriction{AllowOnly: []string{"."}, DenyWithinAllow: []string{}}
			option := MacWrapOptions{Environment: map[string]string{}, Shell: "/bin/bash", Profile: MacProfileOptions{Paths: PathContext{Home: home, CWD: cwd, Realpath: filepath.EvalSymlinks}, Read: read, Write: write, HTTPProxyPort: &h, SOCKSProxyPort: &s, NeedsNetworkRestriction: true}}
			options = append(options, option)
			inputs = append(inputs, command)
			rows = append(rows, map[string]any{"command": command, "needsNetworkRestriction": true, "read": map[string]any{"denyOnly": read.DenyOnly, "allowWithinDeny": read.AllowWithinDeny}, "write": map[string]any{"allowOnly": write.AllowOnly, "denyWithinAllow": write.DenyWithinAllow}, "httpProxyPort": h, "socksProxyPort": s, "binShell": "/bin/bash"})
		}
	}
	options = append(options, MacWrapOptions{Shell: "/unavailable", Profile: MacProfileOptions{Paths: PathContext{Home: home, CWD: cwd, Realpath: filepath.EvalSymlinks}}})
	inputs = append(inputs, "unwrapped")
	rows = append(rows, map[string]any{"command": "unwrapped", "binShell": "/unavailable", "needsNetworkRestriction": false})
	options = append(options, MacWrapOptions{Shell: "/unavailable", Profile: MacProfileOptions{Paths: PathContext{Home: home, CWD: cwd, Realpath: filepath.EvalSymlinks}, NeedsNetworkRestriction: true}})
	inputs = append(inputs, "unavailable")
	rows = append(rows, map[string]any{"command": "unavailable", "binShell": "/unavailable", "needsNetworkRestriction": true})
	fixture, _ := filepath.Abs("testdata/macos-wrap.fixture.ts")
	raw, _ := json.Marshal(map[string]any{"home": home, "cwd": cwd, "rows": rows})
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(raw)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source struct {
		Suffix string
		Rows   []struct {
			Result, Error *string
			Environment   []string
		}
	}
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, option := range options {
		option.SessionSuffix = source.Suffix
		option.FindExecutable = func(name string) (string, error) {
			if name == "/bin/bash" {
				return name, nil
			}
			return "", errors.New("absent PATH executable")
		}
		result, e := WrapMac(inputs[i], option)
		expected := source.Rows[i]
		if (e == nil) != (expected.Error == nil) {
			t.Fatal(i, e, expected.Error)
		}
		if e != nil {
			if e.Error() != *expected.Error {
				t.Fatal(e, *expected.Error)
			}
		} else if expected.Result == nil || result != *expected.Result {
			t.Fatalf("Source%d wrapper changed\n%s\nGo\n%s", i, *expected.Result, result)
		}
		env := ProxyEnvironment(option.Environment, "darwin", option.Profile.HTTPProxyPort, option.Profile.SOCKSProxyPort)
		if !reflect.DeepEqual(env, expected.Environment) {
			t.Fatal(i, env, expected.Environment)
		}
	}
}
