package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
)

func (m *MainPrint) nativeSkillShell(ctx context.Context, scope *cooperative.Scope, request skillcommands.ShellRequest) (string, error) {
	return m.nativeSkillShellWithPermission(ctx, scope, request, nil)
}

// A frontmatter-preloaded Agent skill uses its parent's live getAppState for
// permission, while the actual child Bash service supplies the worker cwd.
// Ordinary slash/Skill invocations continue to read this Harness's own state.
func (m *MainPrint) nativeSkillShellWithPermission(ctx context.Context, scope *cooperative.Scope, request skillcommands.ShellRequest, inherited *protocol.JSONObject) (string, error) {
	value, err := m.nativeSkillShellJSONWithPermission(ctx, scope, request, inherited)
	var text string
	if err == nil {
		err = json.Unmarshal(value, &text)
	}
	return text, err
}

func (m *MainPrint) nativeSkillShellJSONWithPermission(ctx context.Context, scope *cooperative.Scope, request skillcommands.ShellRequest, inherited *protocol.JSONObject) (json.RawMessage, error) {
	if !scope.BelongsTo(m.Owner) || m.nativeTools == nil {
		return nil, cooperative.ErrRestricted
	}
	text := request.TextJSON
	if text == nil {
		text = quoteMain(request.Text)
	}
	matches, e := skillcommands.PromptShellMatchesJSON(text)
	if e != nil {
		return nil, e
	}
	if len(matches) == 0 {
		return text, context.Cause(ctx)
	}
	result := text
	all, e := ownerasync.NewOwnerFuture[json.RawMessage](m.Owner)
	if e != nil {
		return nil, e
	}
	remaining := len(matches)
	failed := false
	finish := func(current *cooperative.Scope, match skillcommands.PromptShellMatch, output string, failure error) error {
		remaining--
		if failure == nil {
			result, failure = skillcommands.ReplacePromptShellMatchJSON(result, match, output)
		}
		if failure != nil && !failed {
			failed = true
			return all.Reject(current, failure)
		}
		if remaining == 0 && !failed {
			return all.Resolve(current, result)
		}
		return nil
	}
	for _, match := range matches {
		_, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(current *cooperative.Scope) error {
			output, failure := m.nativeSkillShellOneWithPermission(ctx, current, request, match, inherited)
			return finish(current, match, output, failure)
		})
		if e != nil {
			return nil, e
		}
	}
	return all.Await(scope)
}

func (m *MainPrint) nativeSkillShellOne(ctx context.Context, scope *cooperative.Scope, request skillcommands.ShellRequest, match skillcommands.PromptShellMatch) (string, error) {
	return m.nativeSkillShellOneWithPermission(ctx, scope, request, match, nil)
}

func (m *MainPrint) nativeSkillShellOneWithPermission(ctx context.Context, scope *cooperative.Scope, request skillcommands.ShellRequest, match skillcommands.PromptShellMatch, inherited *protocol.JSONObject) (string, error) {
	var permission protocol.JSONObject
	var e error
	if inherited != nil {
		permission = inherited.Clone()
	} else {
		permission, e = m.nativeToolPermissionContext(scope)
	}
	if e != nil {
		return "", e
	}
	var rules protocol.JSONObject
	_ = rules.UnmarshalJSON(permission.Get("alwaysAllowRules"))
	if !rules.IsObject() {
		rules = protocol.NewJSONObject()
	}
	rules = rules.Clone()
	rules.Set("command", rawMain(request.AllowedTools))
	permission.Set("alwaysAllowRules", rawMain(rules))
	command := match.CommandJSON
	if command == nil {
		command = quoteMain(match.Command)
	}
	in := protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: command})
	// The original permission call allocates an empty assistant before checking.
	_ = m.factory.CreateAssistant(storage.Object{"content": json.RawMessage("[]")})
	name := "Bash"
	if request.Shell == "powershell" {
		name = "PowerShell"
	}
	var decision queryloop.ToolPermission
	if name == "PowerShell" {
		decision, e = m.nativeTools.PromptPowerShellPermission(scope, &in, permission)
	} else {
		decision, e = m.nativeTools.PromptShellPermission(scope, &in, permission)
	}
	if e != nil {
		return "", e
	}
	if decision.Behavior != "allow" {
		message := decision.Message
		if len(message) == 0 {
			message = quoteMain("Claude requested permissions to use " + name + ", but you haven't granted it yet.")
		}
		var text string
		_ = json.Unmarshal(message, &text)
		if text == "" {
			text = "Permission denied"
		}
		return "", fmt.Errorf("Shell command permission check failed for pattern \"%s\": %s", match.Pattern, text)
	}
	data, e := m.nativeTools.Call(ctx, name, in, scope, false)
	if e != nil {
		var shell *toolerrors.ShellError
		if errors.As(e, &shell) {
			return "", nativeSkillShellFailure(match.Pattern, shell)
		}
		var native *builtintools.CapabilityError
		if errors.As(e, &native) {
			return "", e
		}
		return "", fmt.Errorf("[Error]\n%s", e.Error())
	}
	var mapped protocol.JSONObject
	if name == "PowerShell" {
		mapped, e = m.nativeTools.MapTerminalPowerShellResult(scope, data, quoteMain(uuidMain()))
	} else {
		mapped, e = builtintools.MapResult(name, data, quoteMain(uuidMain()))
	}
	if e != nil {
		return "", e
	}
	mapped, e = queryloop.ProcessSmallTextToolResult(mapped, name, 30_000)
	if e != nil {
		return "", e
	}
	var output string
	if e = json.Unmarshal(mapped.Get("content"), &output); e != nil {
		// Source's prompt utility uses the actual shell streams when the mapped
		// result is structured (for example a normalized image block).
		var shell protocol.JSONObject
		if e := shell.UnmarshalJSON(data); e != nil {
			return "", e
		}
		parts := []string{}
		if stdout := strings.TrimFunc(shell.String("stdout"), skillWhitespace); stdout != "" {
			parts = append(parts, stdout)
		}
		if stderr := strings.TrimFunc(shell.String("stderr"), skillWhitespace); stderr != "" {
			parts = append(parts, "[stderr]\n"+stderr)
		}
		output = strings.Join(parts, "\n")
	}
	return output, nil
}

func nativeSkillShellFailure(pattern string, shell *toolerrors.ShellError) error {
	if shell.Interrupted {
		return fmt.Errorf("Shell command interrupted for pattern \"%s\": [Command interrupted]", pattern)
	}
	parts := []string{}
	if stdout := strings.TrimFunc(shell.Stdout, skillWhitespace); stdout != "" {
		parts = append(parts, stdout)
	}
	if stderr := strings.TrimFunc(shell.Stderr, skillWhitespace); stderr != "" {
		parts = append(parts, "[stderr]\n"+stderr)
	}
	return fmt.Errorf("Shell command failed for pattern \"%s\": %s", pattern, strings.Join(parts, "\n"))
}
