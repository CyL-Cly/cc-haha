package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
)

type processResult struct {
	Out, Err    string
	NewCwd      string
	Code        int
	Interrupted bool
}

func (s *Service) executable(name string) (string, error) {
	return executableInEnvironment(name, s.options.Environment)
}
func executableInEnvironment(name string, environment map[string]string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	for _, dir := range filepath.SplitList(environment["PATH"]) {
		p := filepath.Join(dir, name)
		if st, e := os.Stat(p); e == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			return p, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
func (s *Service) process(ctx context.Context, name string, args []string, cwd string, timeout time.Duration) (processResult, error) {
	return s.processOutput(ctx, name, args, cwd, timeout, false)
}
func (s *Service) processOutput(ctx context.Context, name string, args []string, cwd string, timeout time.Duration, merged bool) (processResult, error) {
	return s.processOutputEnvironment(ctx, name, args, cwd, timeout, merged, s.options.Environment)
}
func (s *Service) processOutputEnvironment(ctx context.Context, name string, args []string, cwd string, timeout time.Duration, merged bool, environment map[string]string) (processResult, error) {
	p, e := executableInEnvironment(name, environment)
	if e != nil {
		return processResult{}, e
	}
	timed, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(timed, p, args...)
	cmd.Dir = cwd
	keys := []string{}
	for k := range environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	cmd.Env = []string{}
	for _, k := range keys {
		cmd.Env = append(cmd.Env, k+"="+environment[k])
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if merged {
		cmd.Stderr = cmd.Stdout
	}
	configureProcess(cmd)
	e = cmd.Run()
	result := processResult{Out: string(protocol.DecodeJavaScriptUTF8(out.Bytes())), Err: string(protocol.DecodeJavaScriptUTF8(errOut.Bytes())), Interrupted: timed.Err() != nil}
	if e != nil {
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			result.Code = exit.ExitCode()
		} else {
			return result, e
		}
	}
	return result, nil
}

func (s *Service) bash(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope) ([]byte, error) {
	if boolean(in, "run_in_background", false) {
		if !skillSimple(s.options.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]) {
			return s.startBackgroundBash(scope, in, nil)
		}
	}
	sandbox, e := s.selectBashSandbox(scope, in)
	if e != nil {
		return nil, e
	}
	command, e := text(in.Get("command"))
	if e != nil {
		return nil, e
	}
	environment, e := s.processEnvironment(scope)
	if e != nil {
		return nil, e
	}
	shell := environment["SHELL"]
	if shell == "" {
		shell = "/bin/bash"
	}
	timeout := nativeBashTimeout(in, environment)
	execution, e := s.prepareBashExecution(ctx, scope, command, "")
	if e != nil {
		return nil, e
	}
	prepareSandboxCommand(&execution, sandbox)
	f, e := native(s, scope, func() (processResult, error) {
		defer execution.cleanupSandbox()
		command, err := execution.resolveShellContext(ctx, shell)
		if err != nil {
			return processResult{}, err
		}
		result, err := s.processOutputEnvironment(ctx, shell, []string{"-l", "-c", command}, execution.OriginalCwd, nativeBashTimerDuration(timeout), true, environment)
		result.NewCwd = execution.finish()
		return result, err
	})
	if e != nil {
		return nil, e
	}
	data, e := f.Await(scope)
	if e != nil {
		return nil, e
	}
	if e = s.consumeBashCwd(scope, execution, data.NewCwd, false); e != nil {
		return nil, e
	}
	failed, e := bashResultIsError(s.options.Platform, command, data.Code)
	if e != nil {
		return nil, e
	}
	// A user cancellation is returned as an interrupted result. Timeouts are
	// failures, as in the original abortController reason comparison.
	if failed && ctx.Err() == nil {
		return nil, &toolerrors.ShellError{Stderr: data.Out, Code: data.Code, Interrupted: data.Interrupted}
	}
	stdout := strings.TrimRightFunc(data.Out, bashSpace) + "\n"
	return object(field("stdout", stdout), field("stderr", data.Err), field("interrupted", data.Interrupted), field("isImage", false), field("noOutputExpected", nativeBashSilent(command, s.options.Platform))), nil
}

func bashSpace(r rune) bool {
	return r >= 9 && r <= 13 || r == 0x20 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}

func trimBashOutput(stdout string) string {
	lastNewline := 0
	for i, r := range stdout {
		if !bashSpace(r) {
			break
		}
		if r == '\n' {
			lastNewline = i + 1
		}
	}
	return strings.TrimRightFunc(stdout[lastNewline:], bashSpace)
}

func bashResultIsError(platform, command string, code int) (bool, error) {
	classifier, e := sessionrewind.NewRecordedCommandClassifier(platform, "external")
	if e != nil {
		return false, e
	}
	parts, e := classifier.SplitForExitCode(command)
	if e != nil {
		return false, e
	}
	last := command
	if len(parts) > 0 && parts[len(parts)-1] != "" {
		last = parts[len(parts)-1]
	}
	words := strings.FieldsFunc(strings.TrimFunc(last, bashSpace), bashSpace)
	base := ""
	if len(words) > 0 {
		base = words[0]
	}
	switch base {
	case "grep", "rg", "find", "diff", "test", "[":
		return code >= 2, nil
	default:
		return code != 0, nil
	}
}

// OriginalBashCallData applies BashTool.call's final stripEmptyLines step to
// the process accumulator. The ASK mapper already consumes this same primitive;
// entrypoints/mcp.ts exposes the tool's data directly instead of mapping it.
func OriginalBashCallData(data json.RawMessage) (json.RawMessage, error) {
	var value protocol.JSONObject
	if err := value.UnmarshalJSON(data); err != nil {
		return nil, err
	}
	value.Set("stdout", quote(trimBashOutput(value.String("stdout"))))
	return value.MarshalJSON()
}
