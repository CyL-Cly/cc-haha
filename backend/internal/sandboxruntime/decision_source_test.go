package sandboxruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestActualSourceShouldUseSandboxExcludedCompoundEnvWrapperAndOverride(t *testing.T) {
	rows := []map[string]any{}
	commands := []string{"", "docker ps", "docker", "echo x && docker ps", "# comment\nFOO=x docker ps", "FOO='x y' docker ps", "FOO='x'y\"z\" docker ps", "FOO+=x docker ps", "FOO[0]=a docker ps", "PATH=/owned docker ps", "LD_PRELOAD=x docker ps", "DYLD_FOO=x docker ps", "FOO=$VALUE docker ps", "timeout 300 FOO=bar docker ps", "FOO=x timeout --kill-after 2s 10 docker ps", "nice -n 5 timeout 2 docker ps", "stdbuf -oL nohup -- docker ps", "time -- docker ps", "timeout -k$(id) 3 docker ps", "timeout\ndocker ps", "echo 'docker ps'", "docker ps; echo x", "docker *", "echo \\\"unterminated", "FOO=x # inline\ndocker ps"}
	for _, command := range commands {
		for _, excluded := range [][]string{{}, {"docker:*"}, {"docker ps"}, {"docker *"}, {`docker \*`}, {"*echo*"}} {
			for _, enabled := range []bool{false, true} {
				for _, allow := range []bool{false, true} {
					for _, override := range []bool{false, true} {
						rows = append(rows, map[string]any{"enabled": enabled, "settings": map[string]any{"sandbox": map[string]any{"allowUnsandboxedCommands": allow, "excludedCommands": excluded}}, "input": map[string]any{"command": command, "dangerouslyDisableSandbox": override}})
					}
				}
			}
		}
	}
	data, _ := json.Marshal(map[string]any{"rows": rows})
	fixture, _ := filepath.Abs("testdata/decision.fixture.ts")
	home := t.TempDir()
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(fixture))))), "preload.ts"), fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "DISABLE_TELEMETRY=1"}
	cmd.Stdin = bytes.NewReader(data)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []bool
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		settings, _ := json.Marshal(row["settings"])
		in := row["input"].(map[string]any)
		actual, e := ShouldUse(testObject(t, string(settings)), row["enabled"].(bool), in["command"].(string), in["dangerouslyDisableSandbox"].(bool))
		if e != nil || actual != source[i] {
			t.Fatalf("Source%d %+v expected%v actual%v error%v", i, row, source[i], actual, e)
		}
	}
}
