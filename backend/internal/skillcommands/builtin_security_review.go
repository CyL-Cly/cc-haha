package skillcommands

import (
	"context"
	_ "embed"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

//go:embed assets/builtin/security-review.md
var securityReviewMarkdown string

func SecurityReviewCommandMetadata() protocol.JSONObject {
	metadata := protocol.NewJSONObject()
	set(metadata, "type", "prompt")
	set(metadata, "name", "security-review")
	set(metadata, "description", "Complete a security review of the pending changes on the current branch")
	set(metadata, "progressMessage", "analyzing code changes for security risks")
	set(metadata, "contentLength", 0)
	set(metadata, "source", "builtin")
	return metadata
}

func NativeSecurityReviewCommand(environment func(Invocation, string) (string, error), resolveShell func(Invocation) (string, error)) (Definition, error) {
	if environment == nil || resolveShell == nil {
		return Definition{}, errors.New("security review requires invoking environment and actual shell resolver")
	}
	return NewNativeCommand(SecurityReviewCommandMetadata(), func(ctx context.Context, args *string, invocation Invocation) (string, error) {
		user, err := environment(invocation, "USER_TYPE")
		if err != nil {
			return "", err
		}
		if user == "ant" {
			return "This command has been moved to a plugin. Tell the user:\n\n1. To install the plugin, run:\n   claude plugin install security-review@claude-code-marketplace\n\n2. After installation, use /security-review:security-review to run this command\n\n3. For more information, see: https://github.com/anthropics/claude-code-marketplace/blob/main/security-review/README.md\n\nDo not attempt to run the command. Simply inform the user about the plugin installation.", ctx.Err()
		}
		shell, err := resolveShell(invocation)
		if err != nil {
			return "", err
		}
		if shell == "" {
			return "", errors.New("No supported command shell is available. Install Git Bash or enable PowerShell.")
		}
		text := securityReviewMarkdown
		if shell != "bash" {
			text = strings.ReplaceAll(text, "Bash(", "PowerShell(")
		}
		parsed, err := markdownfrontmatter.Parse(text)
		if err != nil {
			return "", err
		}
		if invocation.ExecuteShell == nil {
			return "", errors.New("security review requires actual permission-aware prompt shell producer")
		}
		return invocation.ExecuteShell(ctx, ShellRequest{Text: parsed.Content, SlashCommand: "security-review", Shell: shell, AllowedTools: parseTools(parsed.Frontmatter.Get("allowed-tools"))})
	})
}
