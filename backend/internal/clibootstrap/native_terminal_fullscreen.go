package clibootstrap

import (
	"context"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func (m *MainPrint) terminalFullscreenOnOwner(ctx context.Context, scope *cooperative.Scope) (bool, error) {
	environment := m.options.Native.nativeEnvironmentSnapshot()
	flag := strings.ToLower(protocol.TrimJavaScriptString(environment["CLAUDE_CODE_NO_FLICKER"]))
	switch flag {
	case "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	}
	if m.nativeTerminalTmuxControlMode == nil {
		control := environment["TMUX"] != "" && environment["TERM_PROGRAM"] == "iTerm.app" && !strings.HasPrefix(environment["TERM"], "screen") && !strings.HasPrefix(environment["TERM"], "tmux")
		m.nativeTerminalTmuxControlMode = &control
		if control || environment["TMUX"] == "" || environment["TERM_PROGRAM"] != "" {
			return !control && environment["USER_TYPE"] == "ant", nil
		}
		cwd := m.workingDirectory()
		future, err := nativeSkillWork(m, scope, func() (worktree.ProcessResult, error) {
			active, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			return worktree.NativeRun(active, worktree.ProcessRequest{Command: "tmux", Arguments: []string{"display-message", "-p", "#{client_control_mode}"}, CWD: cwd, Environment: environment, IgnoreStdin: true})
		})
		if err != nil {
			return false, err
		}
		output, err := future.Await(scope)
		control = err == nil && output.Code == 0 && protocol.TrimJavaScriptString(output.Stdout) == "1"
		m.nativeTerminalTmuxControlMode = &control
	}
	if ctx.Err() != nil {
		return false, context.Cause(ctx)
	}
	return !*m.nativeTerminalTmuxControlMode && environment["USER_TYPE"] == "ant", nil
}
