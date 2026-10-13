package authhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func TestWindowsAPIHelperShellProjectionSourceConsumerAndPhysicalPeer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	if runtime.GOOS == "windows" {
		t.Skip("this physical peer substitutes POSIX spawn only; native Windows graph is a separate prerequisite")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	nodeExecutable, e := exec.LookPath("node")
	if e != nil {
		t.Fatal("actual local Node source runtime required", e)
	}
	node := exec.CommandContext(ctx, nodeExecutable, "-e", `process.stdout.write(process.binding('natives').child_process)`)
	node.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	spawnSource, e := node.Output()
	if e != nil {
		t.Fatal("actual native Node child-process source required", e)
	}
	type invocation struct {
		Command     string            `json:"command"`
		Environment map[string]string `json:"environment"`
	}
	invocations := []invocation{}
	for _, shell := range []string{"", `C:\Windows\System32\cmd.exe`, `C:\custom\CMD`, "powershell.exe", `C:\private\sh.exe`, `C:/Windows/System32/cmd.exe`} {
		for _, command := range []string{`"C:\private\api helper.cmd" --fake`, `echo "中文 😀" & echo ^%fixture^%`, `private-helper > "C:\private\output.txt"`} {
			invocations = append(invocations, invocation{command, map[string]string{"ComSpec": shell, "PATH": "private-only"}})
		}
	}
	results := []map[string]any{}
	for _, row := range []struct {
		name, command, stdout, stderr string
		code                          int
	}{{"success", "fixture helper", " \nFAKE_PRIVATE_HELPER_KEY\u00a0", "", 0}, {"unicode-whitespace", "fixture helper", "\ufeff 中文 😀 \r\n", "", 0}, {"empty", "fixture helper", " \t\n", "", 0}, {"failed", "fixture helper", "not a successful value", " private diagnostic \r\n", 7}} {
		results = append(results, map[string]any{"name": row.name, "command": row.command, "nonInteractive": true, "output": map[string]any{"stdout": row.stdout, "stderr": row.stderr, "failed": row.code != 0, "exitCode": row.code, "timedOut": false}})
	}
	results = append(results, map[string]any{"name": "trust-refused", "command": "fixture helper", "nonInteractive": false, "project": true, "trusted": false, "output": map[string]any{"stdout": "must not execute", "failed": false, "exitCode": 0}})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-windows-helper-shell.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "nativeSpawnSource": string(spawnSource), "invocations": invocations, "results": results})
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var expected struct {
		Invocations []windowshell.WindowsInvocation
		Results     []struct {
			Value    *string
			Error    string
			Requests []json.RawMessage
		}
	}
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range invocations {
		actual := windowshell.ProjectWindowsShellInvocation(row.Command, row.Environment)
		if !reflect.DeepEqual(actual, expected.Invocations[i]) {
			t.Fatalf("Source native Windows shell argv row%d\nGo%#v\nSource%#v", i, actual, expected.Invocations[i])
		}
	}
	// The actual OS spawn is a private cmd.exe peer. Windows shell projection
	// and the shared Execute consumer remain production code on both sides.
	peer := filepath.Join(home, "cmd.exe")
	script := `#!/bin/sh
printf '%s\n' "$*" > "$HELPER_PRIVATE_HOME/argv.txt"
printf '%s\n' "$PWD" > "$HELPER_PRIVATE_HOME/cwd.txt"
printf '%s' "$HELPER_STDERR" >&2
printf '%s' "$HELPER_STDOUT"
exit "$HELPER_CODE"
`
	if e := os.WriteFile(peer, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	for i, row := range results {
		if row["name"] == "trust-refused" {
			cache := New(Options{})
			value, e := cache.Get(ctx, Request{Command: "fixture helper", ProjectOrLocal: true, TrustAccepted: false, NonInteractive: false, Execute: func(context.Context, string) (string, error) {
				t.Error("untrusted actual Source helper executed")
				return "", nil
			}})
			_ = cache.Close(context.Background())
			if e != nil || value != expected.Results[i].Value || len(expected.Results[i].Requests) != 0 {
				t.Fatal("actual Source trust gate differs", value, e)
			}
			continue
		}
		output := row["output"].(map[string]any)
		env := map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "ComSpec": "cmd.exe", "HELPER_PRIVATE_HOME": home, "HELPER_STDOUT": output["stdout"].(string), "HELPER_STDERR": output["stderr"].(string), "HELPER_CODE": fmtHelperCode(output["exitCode"].(int))}
		actual, e := Execute(ctx, row["command"].(string), ProcessOptions{CWD: home, Environment: env, Command: func(active context.Context, command string, environment map[string]string) (*exec.Cmd, error) {
			projection := windowshell.ProjectWindowsShellInvocation(command, environment)
			return exec.CommandContext(active, peer, projection.Arguments...), nil
		}})
		if expected.Results[i].Error != "" {
			if e == nil || e.Error() != expected.Results[i].Error {
				t.Fatal("actual Source helper process error differs", e, expected.Results[i].Error)
			}
		} else if e != nil || expected.Results[i].Value == nil || actual != *expected.Results[i].Value {
			t.Fatal("actual Source helper stdout differs", actual, e, expected.Results[i].Value)
		}
		cwd, _ := os.ReadFile(filepath.Join(home, "cwd.txt"))
		if strings.TrimSpace(string(cwd)) != home {
			t.Fatal("actual helper did not use private cwd", string(cwd))
		}
		args, _ := os.ReadFile(filepath.Join(home, "argv.txt"))
		if string(args) != "/d /s /c \"fixture helper\"\n" {
			t.Fatal("actual private process did not consume Source Windows argv", string(args))
		}
	}
}
func fmtHelperCode(code int) string {
	if code == 7 {
		return "7"
	}
	return "0"
}
