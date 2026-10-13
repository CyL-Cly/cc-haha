package builtintools

import (
	"context"
	"math"
	"os/exec"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/powershellpolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func (s *Service) powerShellView(scope *cooperative.Scope) (*Service, error) {
	if err := s.check(scope); err != nil {
		return nil, err
	}
	copy := *s
	copy.options = s.options
	copy.shellTool = "PowerShell"
	return &copy, nil
}

func (s *Service) isPowerShell() bool { return s.shellTool == "PowerShell" }

func (s *Service) PowerShellPath(scope *cooperative.Scope) (string, error) {
	if err := s.check(scope); err != nil {
		return "", err
	}
	if s.options.Shells == nil {
		return "", capability("actual prewarmed PowerShell resolver")
	}
	view, warmed := s.options.Shells.Snapshot()
	if !warmed {
		return "", capability("owned PowerShell native discovery before catalog assembly")
	}
	return view.PowerShell, nil
}

func (s *Service) shellExecutable(scope *cooperative.Scope, environment map[string]string) (string, error) {
	if s.isPowerShell() {
		return s.PowerShellPath(scope)
	}
	if windowshell.IsWindows(s.options.Platform) {
		if s.options.Shells == nil {
			return "", capability("actual prewarmed Windows Git Bash resolver")
		}
		view, warmed := s.options.Shells.Snapshot()
		if !warmed {
			return "", capability("owned Git Bash discovery before catalog assembly")
		}
		if view.GitBash == "" {
			return "", s.options.Shells.GitBashUnavailableError()
		}
		return view.GitBash, nil
	}
	shell := environment["SHELL"]
	if shell == "" {
		shell = "/bin/bash"
	}
	return shell, nil
}

func (s *Service) shellArgs(command string) []string {
	if s.isPowerShell() {
		return windowshell.PowerShellArgs(command)
	}
	return []string{"-l", "-c", command}
}

func (s *Service) configureShell(cmd *exec.Cmd) {
	configureShellProcess(cmd)
}

func (s *Service) shellTimeout(input protocol.JSONObject, environment map[string]string) float64 {
	timeout := nativeBashTimeout(input, environment)
	if s.isPowerShell() {
		return math.Min(timeout, nativeBashMaxTimeout(environment))
	}
	return timeout
}

func (s *Service) shellAutoBackgroundAllowed(command string) (bool, error) {
	if s.isPowerShell() {
		words := strings.Fields(protocol.TrimJavaScriptString(command))
		return len(words) == 0 || powershellpolicy.ResolveCanonical(words[0]) != "start-sleep", nil
	}
	return nativeBashAutoBackgroundAllowed(command, s.options.Platform)
}

func (s *Service) shellSandbox(scope *cooperative.Scope, input protocol.JSONObject) (*BashSandboxCommand, error) {
	if s.isPowerShell() && windowshell.IsWindows(s.options.Platform) {
		if err := s.checkWindowsPowerShellSandbox(scope); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return s.selectBashSandbox(scope, input)
}

const WindowsPowerShellSandboxRefusal = "Enterprise policy requires sandboxing, but sandboxing is not available on native Windows. Shell command execution is blocked on this platform by policy."

func (s *Service) checkWindowsPowerShellSandbox(scope *cooperative.Scope) error {
	if err := s.check(scope); err != nil {
		return err
	}
	if s.options.WindowsPowerShellSandboxPolicy != nil {
		return s.options.WindowsPowerShellSandboxPolicy(scope)
	}
	if binding := s.options.BashSandbox; binding != nil {
		if binding.Owner != s.options.Owner || binding.PowerShellPolicy == nil {
			return capability("actual PowerShell native Windows sandbox policy")
		}
		return binding.PowerShellPolicy(scope)
	}
	return nil
}

// Parser runners call this off-owner through the caller's actual NativeBinding.
// It preserves both streams, physical exit status and the timeout/caller abort
// distinction; it does not parse PowerShell source with Bash heuristics.
func (s *Service) RunPowerShellPrimitive(ctx context.Context, executable string, args []string, cwd string, environment map[string]string) (stdout, stderr string, exitCode int, timedOut bool, err error) {
	result, err := s.processOutputEnvironment(ctx, executable, args, cwd, 5*time.Second, false, environment)
	if ctx.Err() == context.DeadlineExceeded {
		return result.Out, result.Err, result.Code, true, nil
	}
	return result.Out, result.Err, result.Code, result.Interrupted && ctx.Err() == nil, err
}
