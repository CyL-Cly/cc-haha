package clibootstrap

import (
	"context"
	"fmt"
	"regexp"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

var embeddedPromptBlock = regexp.MustCompile("(?s)```!\\s*\\n?(.*?)\\n?```")
var embeddedPromptInline = regexp.MustCompile("(^|[\\t\\n\\r\\f\\v \\x{00a0}\\x{1680}\\x{2000}-\\x{200a}\\x{2028}\\x{2029}\\x{202f}\\x{205f}\\x{3000}\\x{feff}])!`[^`]+`")

func (m *MainPrint) nativeSkillInvocation(scope *cooperative.Scope) (skillcommands.Invocation, error) {
	if m.nativeSettings == nil || m.pluginOptions == nil || !scope.BelongsTo(m.Owner) {
		return skillcommands.Invocation{}, cooperative.ErrRestricted
	}
	platform := m.options.Platform
	if platform == "windows" {
		platform = "win32"
	}
	n := m.options.Native
	return skillcommands.Invocation{NativeScope: scope, SessionID: m.Controller.SessionID(), Platform: platform, PluginDataDirectory: func(ctx context.Context, id string) (string, error) { return nativePluginDataDirectory(n, ctx, id) }, PluginOptions: m.pluginOptions.Load, ExecuteShell: func(ctx context.Context, request skillcommands.ShellRequest) (string, error) {
		if err := m.validateNativePromptShell(scope, request); err != nil {
			return "", err
		}
		if embeddedPromptBlock.MatchString(request.Text) || embeddedPromptInline.MatchString(request.Text) {
			return m.nativeSkillShell(ctx, scope, request)
		}
		return request.Text, context.Cause(ctx)
	}}, nil
}

func (m *MainPrint) validateNativePromptShell(scope *cooperative.Scope, request skillcommands.ShellRequest) error {
	availability, err := m.nativeShellAvailability(scope)
	if err != nil {
		return err
	}
	if request.Shell == "powershell" {
		if !availability.PowerShell {
			return fmt.Errorf("PowerShell is required to execute commands in %s. Install or enable PowerShell, or declare `shell: bash` and use Bash syntax.", request.SlashCommand)
		}
	} else if !availability.Bash {
		return fmt.Errorf("Bash is required to execute commands in %s. Install Git Bash or declare `shell: powershell` and use PowerShell syntax.", request.SlashCommand)
	}
	return nil
}
