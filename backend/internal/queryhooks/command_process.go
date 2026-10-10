package queryhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type CommandProcessRequest struct {
	AllowAsync                              bool
	OutputDirectory                         string
	Progress                                func(CommandOutput) error
	Hook                                    protocol.JSONObject
	Input                                   protocol.JSONObject
	CWD, OriginalCWD, ProjectRoot, Platform string
	Environment                             map[string]string
	NowMillis                               func() float64
	ShellResolver                           *CommandShellResolver
}
type hookCapturedOutput struct {
	mu                     sync.Mutex
	stdout, stderr, output bytes.Buffer
}
type hookOutputPipe struct {
	all    *hookCapturedOutput
	stderr bool
}

func (p hookOutputPipe) Write(data []byte) (int, error) {
	p.all.mu.Lock()
	defer p.all.mu.Unlock()
	if p.stderr {
		_, _ = p.all.stderr.Write(data)
	} else {
		_, _ = p.all.stdout.Write(data)
	}
	return p.all.output.Write(data)
}

// RunCommandProcess is only the native subprocess primitive. Selection,
// registration, hook input, output validation and continuation live above it.
// The shared Harness owns its native reference and awaits this actual join.
func RunCommandProcess(ctx context.Context, r CommandProcessRequest) (CommandOutput, error) {
	if r.AllowAsync {
		return runAsynchronousCommandProcess(ctx, r)
	}
	out := CommandOutput{}
	if r.Platform == "windows" || r.Platform == "win32" {
		return out, unsupported("Windows command hook shell producer")
	}
	if r.NowMillis == nil {
		return out, errors.New("hook process requires actual clock")
	}
	cwd := r.CWD
	if info, e := os.Stat(cwd); e != nil || !info.IsDir() {
		cwd = r.OriginalCWD
	}
	timeout := float64(10 * 60 * 1000)
	var seconds float64
	if json.Unmarshal(r.Hook.Get("timeout"), &seconds) == nil && seconds != 0 {
		timeout = seconds * 1000
	}
	if timeout < 0 {
		timeout = 0
	}
	active, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	text := r.Hook.String("command")
	var command *exec.Cmd
	if r.Hook.String("shell") == "powershell" {
		if r.ShellResolver == nil {
			return out, unsupported("actual cached PowerShell path producer")
		}
		path := r.ShellResolver.PowerShell()
		if path == "" {
			return out, fmt.Errorf("Hook %q has shell: 'powershell' but no PowerShell executable (pwsh or powershell) was found on PATH. Install PowerShell, or remove \"shell\": \"powershell\" to use bash.", text)
		}
		command = exec.CommandContext(active, path, "-NoProfile", "-NonInteractive", "-Command", text)
	} else {
		if prefix := r.Environment["CLAUDE_CODE_SHELL_PREFIX"]; prefix != "" {
			var e error
			text, e = FormatHookShellPrefix(prefix, text)
			if e != nil {
				return out, e
			}
		}
		command = exec.CommandContext(active, "/bin/sh", "-c", text)
	}
	command.Dir = cwd
	env := map[string]string{}
	for k, v := range r.Environment {
		env[k] = v
	}
	env["CLAUDE_PROJECT_DIR"] = r.ProjectRoot
	keys := []string{}
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	command.Env = []string{}
	for _, k := range keys {
		command.Env = append(command.Env, k+"="+env[k])
	}
	input, e := r.Input.MarshalJSON()
	if e != nil {
		return out, e
	}
	command.Stdin = strings.NewReader(string(protocol.StringifyValidatedJavaScriptJSON(input)) + "\n")
	captured := &hookCapturedOutput{}
	command.Stdout = hookOutputPipe{all: captured}
	command.Stderr = hookOutputPipe{all: captured, stderr: true}
	configureHookProcess(command)
	start := r.NowMillis()
	stopProgress, progressDone := make(chan struct{}), make(chan struct{})
	if r.Progress != nil {
		go func() {
			defer close(progressDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			last := ""
			for {
				select {
				case <-stopProgress:
					return
				case <-ticker.C:
					captured.mu.Lock()
					snapshot := CommandOutput{Stdout: string(protocol.DecodeJavaScriptUTF8(captured.stdout.Bytes())), Stderr: string(protocol.DecodeJavaScriptUTF8(captured.stderr.Bytes())), Output: string(protocol.DecodeJavaScriptUTF8(captured.output.Bytes()))}
					captured.mu.Unlock()
					if snapshot.Output != last {
						last = snapshot.Output
						_ = r.Progress(snapshot)
					}
				}
			}
		}()
	} else {
		close(progressDone)
	}
	e = command.Run()
	close(stopProgress)
	<-progressDone
	out = CommandOutput{Stdout: string(protocol.DecodeJavaScriptUTF8(captured.stdout.Bytes())), Stderr: string(protocol.DecodeJavaScriptUTF8(captured.stderr.Bytes())), Output: string(protocol.DecodeJavaScriptUTF8(captured.output.Bytes())), Aborted: active.Err() != nil, DurationMillis: r.NowMillis() - start}
	if e != nil {
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			out.Code = exit.ExitCode()
		} else {
			return out, e
		}
	}
	return out, nil
}
