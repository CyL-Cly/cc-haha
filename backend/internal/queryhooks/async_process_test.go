package queryhooks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func asyncPhysicalRequest(t *testing.T, code string) CommandProcessRequest {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Hook process domain")
	}
	home := t.TempDir()
	script := filepath.Join(home, "hook.py")
	if err := os.WriteFile(script, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	return CommandProcessRequest{AllowAsync: true, OutputDirectory: filepath.Join(home, "tasks"), Hook: protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: commandJSON("/usr/bin/python3 " + strconv.Quote(script))}), Input: protocol.NewJSONObject(protocol.JSONField{Name: "prompt", Value: commandJSON("actual-input")}), CWD: home, OriginalCWD: home, ProjectRoot: home, Platform: runtime.GOOS, Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}, NowMillis: func() float64 { return float64(time.Now().UnixMilli()) }}
}
func waitAsyncPhysical(t *testing.T, process AsyncProcess) {
	t.Helper()
	select {
	case <-process.Done():
	case <-time.After(10 * time.Second):
		process.Kill()
		t.Fatal("actual async Hook did not finish")
	}
	process.Join()
}
func TestAsyncCommandPhysicalTransferSurvivesTurnCancelAndTimeout(t *testing.T) {
	r := asyncPhysicalRequest(t, "import json,sys,time,os\nx=json.load(sys.stdin)\ntime.sleep(.2)\nprint('\\n'+json.dumps({'systemMessage':x['prompt'],'hookSpecificOutput':{'additionalContext':'physical-async'}}),flush=True)\nsys.stderr.write('late physical stderr\\n')\n")
	r.Hook.Set("async", commandJSON(true))
	r.Hook.Set("timeout", commandJSON(.03))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	out, err := RunCommandProcess(ctx, r)
	if err != nil || !out.Backgrounded || out.AsyncProcess == nil || time.Since(started) > time.Second {
		t.Fatal("configured async did not transfer actual process", out, err)
	}
	cancel()
	waitAsyncPhysical(t, out.AsyncProcess)
	owner := cooperative.New()
	defer owner.Close()
	events := 0
	registry, err := NewAsyncRegistry(AsyncRegistryOptions{Owner: owner, NowMillis: r.NowMillis, Event: func(_ *cooperative.Scope, _ AsyncHook, out CommandOutput, kind string) error {
		events++
		if kind != "success" || out.Code != 0 {
			return fmt.Errorf("actual detached output changed: %+v %s", out, kind)
		}
		return nil
	}, Notify: func(*cooperative.Scope, string) error { return errors.New("ordinary async must not enqueue rewake") }, Invalidate: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	var responses []protocol.JSONObject
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := registry.Register(scope, AsyncHook{ProcessID: "physical", HookID: "physical-hook", HookName: "PostToolUse:Write", HookEvent: "PostToolUse", Process: out.AsyncProcess}); err != nil {
			return err
		}
		var err error
		responses, err = registry.Responses(scope)
		return err
	})
	if err != nil || len(responses) != 1 || events != 1 {
		t.Fatal("actual async response not consumed", responses, events, err)
	}
	response, _ := responses[0].Object("response")
	if response.String("systemMessage") != "actual-input" || responses[0].String("stderr") != "" || !strings.Contains(responses[0].String("stdout"), "Output truncated (") {
		t.Fatal("original spilled pipe-mode projection changed", responses)
	}
	files, err := filepath.Glob(filepath.Join(r.OutputDirectory, "hook_*.output"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	disk, err := os.ReadFile(files[0])
	if err != nil || !strings.Contains(string(disk), "physical-async") || !strings.Contains(string(disk), "[stderr] late physical stderr") {
		t.Fatal("actual async output was not physically retained", string(disk), err)
	}
	if err = registry.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestAsyncCommandFirstLineProtocolAndNonAsyncFirstLine(t *testing.T) {
	for _, async := range []bool{true, false} {
		t.Run(strconv.FormatBool(async), func(t *testing.T) {
			first := "{\"async\":true,\"asyncTimeout\":9}"
			if !async {
				first = "{\"systemMessage\":\"synchronous\"}"
			}
			r := asyncPhysicalRequest(t, fmt.Sprintf("import sys,time\nprint(%q,flush=True)\ntime.sleep(.15)\nprint('\\n{\"systemMessage\":\"completed\"}',flush=True)\n", first))
			out, err := RunCommandProcess(context.Background(), r)
			if err != nil || out.Backgrounded != async {
				t.Fatal("first-line protocol changed", out, err)
			}
			if async {
				if out.AsyncTimeout != 9 {
					t.Fatal(out.AsyncTimeout)
				}
				waitAsyncPhysical(t, out.AsyncProcess)
				output, status, err := out.AsyncProcess.Snapshot()
				if err != nil || status != "completed" || !strings.Contains(output.Stdout, "completed") {
					t.Fatal(output, status, err)
				}
				out.AsyncProcess.Cleanup()
			}
		})
	}
}
func TestAsyncRewakeRetainsInterruptPolicyAndNotification(t *testing.T) {
	for _, hard := range []bool{false, true} {
		t.Run(strconv.FormatBool(hard), func(t *testing.T) {
			r := asyncPhysicalRequest(t, "import sys,time\ntime.sleep(.15)\nsys.stderr.write('physical rewake stderr')\nsys.stdout.write('physical rewake stdout')\nsys.exit(2)\n")
			r.Hook.Set("asyncRewake", commandJSON(true))
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			out, err := RunCommandProcess(ctx, r)
			if err != nil || !out.Backgrounded || !out.AsyncRewake {
				t.Fatal(out, err)
			}
			if hard {
				cancel(errors.New("hard cancel"))
			} else {
				cancel(errors.New("interrupt"))
			}
			waitAsyncPhysical(t, out.AsyncProcess)
			output, status, err := out.AsyncProcess.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if hard {
				if status != "killed" {
					t.Fatal(status)
				}
				out.AsyncProcess.Cleanup()
				return
			}
			if status != "completed" || output.Code != 2 || output.Stderr != "physical rewake stderr" {
				t.Fatal(output, status)
			}
			owner := cooperative.New()
			defer owner.Close()
			notification := make(chan string, 1)
			registry, err := NewAsyncRegistry(AsyncRegistryOptions{Owner: owner, NowMillis: r.NowMillis, Event: func(*cooperative.Scope, AsyncHook, CommandOutput, string) error { return nil }, Notify: func(_ *cooperative.Scope, text string) error { notification <- text; return nil }, Invalidate: func() {}})
			if err != nil {
				t.Fatal(err)
			}
			if err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				return registry.Register(scope, AsyncHook{ProcessID: "rewake", HookID: "hook-rewake", HookName: "Stop", HookEvent: "Stop", Process: out.AsyncProcess, Rewake: true})
			}); err != nil {
				t.Fatal(err)
			}
			select {
			case text := <-notification:
				if text != "<system-reminder>\nStop hook blocking error from command \"Stop\": physical rewake stderr\n</system-reminder>" {
					t.Fatal(text)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("actual rewake did not reach original notification callback")
			}
			if err = registry.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestAsyncRegistryCloseKillsAndJoinsPhysicalProcessTree(t *testing.T) {
	r := asyncPhysicalRequest(t, "import os,subprocess,time\np=subprocess.Popen(['/bin/sleep','60'])\nwith open('pids','w') as f:f.write(str(os.getpid())+' '+str(p.pid))\ntime.sleep(60)\n")
	r.Hook.Set("async", commandJSON(true))
	out, err := RunCommandProcess(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	pids := ""
	deadline := time.Now().Add(10 * time.Second)
	for pids == "" && time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(r.CWD, "pids")); err == nil {
			pids = string(data)
		}
		time.Sleep(time.Millisecond)
	}
	if pids == "" {
		out.AsyncProcess.Kill()
		t.Fatal("actual process/grandchild did not start")
	}
	owner := cooperative.New()
	defer owner.Close()
	events := 0
	registry, err := NewAsyncRegistry(AsyncRegistryOptions{Owner: owner, NowMillis: r.NowMillis, Event: func(_ *cooperative.Scope, _ AsyncHook, out CommandOutput, kind string) error {
		events++
		if kind != "cancelled" || out.Code != 1 {
			return fmt.Errorf("Source finalize result changed: %+v %s", out, kind)
		}
		return nil
	}, Notify: func(*cooperative.Scope, string) error { return nil }, Invalidate: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return registry.Register(scope, AsyncHook{ProcessID: "tree", HookID: "hook-tree", HookName: "Stop", HookEvent: "Stop", Process: out.AsyncProcess})
	}); err != nil {
		t.Fatal(err)
	}
	if err = registry.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatal(events)
	}
	for _, raw := range strings.Fields(pids) {
		pid, err := strconv.Atoi(raw)
		if err != nil || hookProcessAlive(pid) {
			t.Fatalf("Close left actual descendant %d running: %v", pid, err)
		}
	}
}
