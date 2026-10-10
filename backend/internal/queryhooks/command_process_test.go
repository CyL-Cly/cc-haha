package queryhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestCommandHookPhysicalStdinEnvironmentCWDAndExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process domain")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "worker")
	_ = os.Mkdir(cwd, 0700)
	script := filepath.Join(home, "hook.py")
	code := "import sys,json,os\nx=json.load(sys.stdin)\nprint(json.dumps({'input':x,'cwd':os.getcwd(),'project':os.environ.get('CLAUDE_PROJECT_DIR'),'home':os.environ.get('HOME')}))\nsys.stderr.write('exact stderr\\n')\nsys.exit(2)\n"
	if e := os.WriteFile(script, []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	hook := protocol.NewJSONObject()
	hook.Set("command", commandJSON("/usr/bin/python3 "+strconv.Quote(script)))
	input := protocol.NewJSONObject()
	input.Set("hook_event_name", commandJSON("SubagentStop"))
	input.Set("future", json.RawMessage(`{"keep":true}`))
	out, e := RunCommandProcess(context.Background(), CommandProcessRequest{Hook: hook, Input: input, CWD: cwd, OriginalCWD: home, ProjectRoot: home, Platform: runtime.GOOS, Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}, NowMillis: func() float64 { return float64(time.Now().UnixMilli()) }})
	var data protocol.JSONObject
	parse := data.UnmarshalJSON([]byte(out.Stdout))
	physical, _ := filepath.EvalSymlinks(cwd)
	if e != nil || parse != nil || out.Code != 2 || out.Stderr != "exact stderr\n" || data.String("home") != home || data.String("project") != home || data.String("cwd") != physical {
		t.Fatalf("actual process lost Source inputs %+v %v %v", out, e, parse)
	}
	actual, _ := data.Object("input")
	future, _ := actual.Object("future")
	if actual.String("hook_event_name") != "SubagentStop" || string(future.Get("keep")) != "true" {
		t.Fatal("actual stdin changed", out.Stdout)
	}
}

func TestCommandHookCancellationJoinsPhysicalProcessAndItsGrandchild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process domain")
	}
	home := t.TempDir()
	pidFile := filepath.Join(home, "pids")
	script := filepath.Join(home, "hook.py")
	code := fmt.Sprintf("import os,subprocess,time\np=subprocess.Popen(['/bin/sleep','60'])\nwith open(%q,'w') as f:f.write(str(os.getpid())+' '+str(p.pid))\ntime.sleep(60)\n", pidFile)
	if e := os.WriteFile(script, []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	hook := protocol.NewJSONObject()
	hook.Set("command", commandJSON("/usr/bin/python3 "+strconv.Quote(script)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan CommandOutput, 1)
	failure := make(chan error, 1)
	go func() {
		out, e := RunCommandProcess(ctx, CommandProcessRequest{Hook: hook, Input: protocol.NewJSONObject(), CWD: home, OriginalCWD: home, ProjectRoot: home, Platform: runtime.GOOS, Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}, NowMillis: func() float64 { return float64(time.Now().UnixMilli()) }})
		failure <- e
		done <- out
	}()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	var pids string
	for pids == "" {
		data, e := os.ReadFile(pidFile)
		if e == nil {
			pids = string(data)
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("actual hook/grandchild did not start")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case out := <-done:
		if e := <-failure; e != nil || !out.Aborted {
			t.Fatal("cancel did not return actual interrupted process", out, e)
		}
	case <-deadline.C:
		t.Fatal("actual process did not join cancellation")
	}
	for _, raw := range strings.Fields(pids) {
		pid, e := strconv.Atoi(raw)
		if e != nil {
			t.Fatal(e)
		}
		if hookProcessAlive(pid) {
			t.Fatal("cancel left actual descendant alive", pid)
		}
	}
}
