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
	if !scope.BelongsTo(m.Owner) || m.nativeTools == nil {
		return "", cooperative.ErrRestricted
	}
	if request.Shell == "powershell" {
		return "", mainCap("actual PowerShell prompt tool consumer")
	}
	matches, e := skillcommands.PromptShellMatches(request.Text)
	if e != nil {
		return "", e
	}
	if len(matches) == 0 {
		return request.Text, context.Cause(ctx)
	}
	result := request.Text
	all, e := ownerasync.NewOwnerFuture[string](m.Owner)
	if e != nil {
		return "", e
	}
	remaining := len(matches)
	failed := false
	finish := func(current *cooperative.Scope, match skillcommands.PromptShellMatch, output string, failure error) error {
		remaining--
		if failure != nil && !failed {
			failed = true
			return all.Reject(current, failure)
		}
		if failure == nil {
			result = strings.Replace(result, match.Pattern, output, 1)
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
			return "", e
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
	in := protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: quoteMain(match.Command)})
	// The original permission call allocates an empty assistant before checking.
	_ = m.factory.CreateAssistant(storage.Object{"content": json.RawMessage("[]")})
	decision, e := m.nativeTools.PromptShellPermission(scope, &in, permission)
	if e != nil {
		return "", e
	}
	if decision.Behavior != "allow" {
		message := decision.Message
		if len(message) == 0 {
			message = quoteMain("Claude requested permissions to use Bash, but you haven't granted it yet.")
		}
		var text string
		_ = json.Unmarshal(message, &text)
		if text == "" {
			text = "Permission denied"
		}
		return "", fmt.Errorf("Shell command permission check failed for pattern \"%s\": %s", match.Pattern, text)
	}
	data, e := m.nativeTools.Call(ctx, "Bash", in, scope, false)
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
	mapped, e := builtintools.MapResult("Bash", data, quoteMain(uuidMain()))
	if e != nil {
		return "", e
	}
	mapped, e = queryloop.ProcessSmallTextToolResult(mapped, "Bash", 30_000)
	if e != nil {
		return "", e
	}
	var output string
	if e = json.Unmarshal(mapped.Get("content"), &output); e != nil {
		return "", e
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
