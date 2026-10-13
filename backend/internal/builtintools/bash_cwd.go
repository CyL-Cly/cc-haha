package builtintools

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
	"golang.org/x/text/unicode/norm"
	"os"
	"path/filepath"
	"strings"
)

type BashCwdBindings struct {
	Owner              *cooperative.Owner
	TemporaryDirectory string
	Changed            func(*cooperative.Scope, string, string) error
	PreventChanges     bool
}
type bashExecution struct {
	sandbox                    *BashSandboxCommand
	Command, Path, OriginalCwd string
	PowerShell, Windows        bool
	script                     func() (*string, error)
}

func (s *Service) prepareBashExecution(ctx context.Context, scope *cooperative.Scope, command, id string) (bashExecution, error) {
	execution := command
	var script func() (*string, error)
	var err error
	if s.isPowerShell() {
		// PowerShell receives the session environment as native child env;
		// the captured Bash export script is not PowerShell source.
	} else if s.options.SessionEnvironmentCapture != nil {
		script, err = s.options.SessionEnvironmentCapture(scope)
	} else {
		execution, err = s.sessionEnvironmentCommand(ctx, scope, command)
	}
	if err != nil {
		return bashExecution{}, err
	}
	result := bashExecution{Command: execution, OriginalCwd: s.options.CWD, script: script, PowerShell: s.isPowerShell(), Windows: windowshell.IsWindows(s.options.Platform)}
	binding := s.options.BashCwd
	if binding == nil && !s.isPowerShell() {
		return result, nil
	}
	if binding != nil && (binding.Owner != s.options.Owner || binding.Changed == nil || !scope.BelongsTo(binding.Owner) || !filepath.IsAbs(binding.TemporaryDirectory)) {
		return result, cooperative.ErrRestricted
	}
	if id == "" {
		id, err = taskIdentity('s')
		if err != nil {
			return result, err
		}
	}
	temporary := ""
	if binding != nil {
		temporary = binding.TemporaryDirectory
	} else {
		temporary = s.options.Environment["TMPDIR"]
		if temporary == "" {
			temporary = s.options.Environment["TEMP"]
		}
		if temporary == "" {
			temporary = s.options.Environment["TMP"]
		}
		if temporary == "" {
			return result, capability("explicit native PowerShell cwd temporary directory")
		}
	}
	result.Path = filepath.Join(temporary, "claude-"+id+"-cwd")
	if s.isPowerShell() {
		result.Path = filepath.Join(temporary, "claude-pwd-ps-"+id)
		return result, nil
	}
	if execution == command && script == nil {
		execution = "eval " + bashQuote(command)
	}
	result.Command = execution
	return result, nil
}

func (execution bashExecution) resolve() (string, error) {
	command := execution.Command
	if execution.PowerShell {
		return windowshell.PowerShellCommand(command, execution.Path), nil
	}
	if execution.script != nil {
		script, err := execution.script()
		if err != nil {
			return "", err
		}
		command = "eval " + bashQuote(command)
		if script != nil && *script != "" {
			command = *script + " && " + command
		}
	}
	if execution.Path != "" {
		path := execution.Path
		if execution.Windows {
			path = nativepaths.WindowsToPOSIX(path)
		}
		command += " && pwd -P >| " + bashQuote(path)
	}
	return command, nil
}
func bashQuote(text string) string { return "'" + strings.ReplaceAll(text, "'", "'\\''") + "'" }

// Read/remove happens in the same actual native process completion producer;
// the owner then updates cwd before returning the original foreground result.
func (execution bashExecution) finish() string {
	if execution.Path == "" {
		return ""
	}
	defer os.Remove(execution.Path)
	data, err := os.ReadFile(execution.Path)
	if err != nil {
		return ""
	}
	cwd := strings.TrimFunc(string(protocol.DecodeJavaScriptUTF8(data)), bashSpace)
	if execution.Windows && !execution.PowerShell {
		cwd = nativepaths.POSIXToWindows(cwd)
	}
	if norm.NFC.String(cwd) == norm.NFC.String(execution.OriginalCwd) {
		return ""
	}
	return cwd
}
func (s *Service) consumeBashCwd(scope *cooperative.Scope, execution bashExecution, cwd string, background bool) error {
	if background || cwd == "" || s.options.BashCwd == nil || s.options.BashCwd.PreventChanges {
		return nil
	}
	return s.options.BashCwd.Changed(scope, execution.OriginalCwd, cwd)
}
