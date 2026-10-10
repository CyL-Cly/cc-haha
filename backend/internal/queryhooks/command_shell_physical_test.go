package queryhooks

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPowerShellCommandHookStartsNativeResolvedExecutableWithExactArgsAndPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable primitive")
	}
	home := t.TempDir()
	path := filepath.Join(home, "pwsh")
	// Only the native executable boundary is substituted: it records actual
	// argv/stdin/env. No PowerShell language or Hook business is simulated.
	code := "#!/usr/bin/python3\nimport sys,json,os\nx=json.load(sys.stdin)\nprint(json.dumps({'args':sys.argv[1:],'input':x,'project':os.environ.get('CLAUDE_PROJECT_DIR')}))\n"
	if e := os.WriteFile(path, []byte(code), 0700); e != nil {
		t.Fatal(e)
	}
	env := map[string]string{"HOME": home, "PATH": home, "CLAUDE_CODE_SHELL_PREFIX": "should never run"}
	resolver := NewCommandShellResolver(env, runtime.GOOS)
	hook := protocol.NewJSONObject(protocol.JSONField{Name: "shell", Value: commandJSON("powershell")}, protocol.JSONField{Name: "command", Value: commandJSON("$env:X = 'actual'; Write-Output $env:X")})
	input := protocol.NewJSONObject(protocol.JSONField{Name: "hook_event_name", Value: commandJSON("Stop")})
	out, e := RunCommandProcess(context.Background(), CommandProcessRequest{Hook: hook, Input: input, CWD: home, OriginalCWD: home, ProjectRoot: home, Platform: runtime.GOOS, Environment: env, ShellResolver: resolver, NowMillis: func() float64 { return float64(time.Now().UnixMilli()) }})
	var recorded struct {
		Args    []string
		Input   protocol.JSONObject
		Project string
	}
	parse := json.Unmarshal([]byte(out.Stdout), &recorded)
	if e != nil || parse != nil || out.Code != 0 || len(recorded.Args) != 4 || recorded.Args[0] != "-NoProfile" || recorded.Args[1] != "-NonInteractive" || recorded.Args[2] != "-Command" || recorded.Args[3] != hook.String("command") || recorded.Input.String("hook_event_name") != "Stop" || recorded.Project != home {
		t.Fatal("actual PowerShell subprocess boundary", out, e, parse)
	}
	hook.Set("shell", commandJSON("bash"))
	hook.Set("command", commandJSON("printf 'actual prefix'"))
	env["CLAUDE_CODE_SHELL_PREFIX"] = "/bin/sh -c"
	out, e = RunCommandProcess(context.Background(), CommandProcessRequest{Hook: hook, Input: input, CWD: home, OriginalCWD: home, ProjectRoot: home, Platform: runtime.GOOS, Environment: env, NowMillis: func() float64 { return float64(time.Now().UnixMilli()) }})
	if e != nil || out.Code != 0 || out.Stdout != "actual prefix" {
		t.Fatal("actual Bash prefix process", out, e)
	}
}
