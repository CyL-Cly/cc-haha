package builtintools

import (
	"context"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Policy is captured on the actual owner; only native wrapping/cleanup is
// passed to the physical command producer. It cannot mint a new tool grant.
type BashSandboxBindings struct {
	Owner            *cooperative.Owner
	PowerShellPolicy func(*cooperative.Scope) error
	// Nil means the sandbox is disabled. The actual initialized runtime supplies
	// its write configuration; permission evaluation does not invent write paths.
	WritePaths func(*cooperative.Scope) (*BashSandboxWritePaths, error)
	Select     func(*cooperative.Scope, protocol.JSONObject) (*BashSandboxCommand, error)
	AutoAllow  func(*cooperative.Scope, protocol.JSONObject) (bool, error)
}
type BashSandboxWritePaths struct {
	AllowOnly, DenyWithinAllow []string
}
type BashSandboxCommand struct {
	TemporaryDirectory string
	Wrap               func(string, string) (string, error)
	WrapContext        func(context.Context, string, string) (string, error)
	Cleanup            func() error
}

func (s *Service) HasSandboxBindings() bool {
	if s == nil {
		return false
	}
	b := s.options.BashSandbox
	return b != nil && b.Owner == s.options.Owner && b.Select != nil && b.AutoAllow != nil
}

func (s *Service) selectBashSandbox(scope *cooperative.Scope, in protocol.JSONObject) (*BashSandboxCommand, error) {
	if binding := s.options.BashSandbox; binding != nil {
		if !scope.BelongsTo(binding.Owner) || binding.Owner != s.options.Owner || binding.Select == nil {
			return nil, cooperative.ErrRestricted
		}
		command, e := binding.Select(scope, in)
		if e != nil {
			return nil, e
		}
		if command != nil && (!filepath.IsAbs(command.TemporaryDirectory) || command.Wrap == nil || command.Cleanup == nil) {
			return nil, cooperative.ErrRestricted
		}
		return command, nil
	}
	if boolean(in, "dangerouslyDisableSandbox", false) {
		return nil, capability("Bash sandbox override authorization")
	}
	return nil, nil
}
func prepareSandboxCommand(execution *bashExecution, sandbox *BashSandboxCommand) {
	if sandbox == nil {
		return
	}
	execution.sandbox = sandbox
	if execution.Path != "" {
		execution.Path = filepath.Join(sandbox.TemporaryDirectory, filepath.Base(execution.Path))
	}
}
func (execution bashExecution) resolveShell(shell string) (string, error) {
	return execution.resolveShellContext(context.Background(), shell)
}
func (execution bashExecution) resolveShellContext(ctx context.Context, shell string) (string, error) {
	command, e := execution.resolve()
	if e != nil {
		return "", e
	}
	if execution.sandbox == nil {
		return command, nil
	}
	var wrapped string
	if execution.sandbox.WrapContext != nil {
		wrapped, e = execution.sandbox.WrapContext(ctx, command, shell)
	} else {
		wrapped, e = execution.sandbox.Wrap(command, shell)
	}
	if e != nil {
		return "", e
	}
	// Source mkdir failure is diagnostic only; the sandbox remains enforced.
	_ = os.MkdirAll(execution.sandbox.TemporaryDirectory, 0700)
	return wrapped, nil
}
func (execution bashExecution) cleanupSandbox() {
	if execution.sandbox != nil {
		_ = execution.sandbox.Cleanup()
	}
}
