package skillcommands

import (
	"context"
	_ "embed"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Authored Source prompt; interpolation remains owned by the Go producer.
//
//go:embed assets/builtin/pr-comments.md
var prCommentsPrompt string

func PRCommentsCommandMetadata() protocol.JSONObject {
	metadata := protocol.NewJSONObject()
	set(metadata, "type", "prompt")
	set(metadata, "name", "pr-comments")
	set(metadata, "description", "Get comments from a GitHub pull request")
	set(metadata, "progressMessage", "fetching PR comments")
	set(metadata, "contentLength", 0)
	set(metadata, "source", "builtin")
	return metadata
}

func NativePRCommentsCommand(environment func(Invocation, string) (string, error)) (Definition, error) {
	if environment == nil {
		return Definition{}, errors.New("PR comments command requires actual invoking process environment")
	}
	return NewNativeCommand(PRCommentsCommandMetadata(), func(ctx context.Context, args *string, invocation Invocation) (string, error) {
		userType, err := environment(invocation, "USER_TYPE")
		if err != nil {
			return "", err
		}
		if userType == "ant" {
			return "This command has been moved to a plugin. Tell the user:\n\n1. To install the plugin, run:\n   claude plugin install pr-comments@claude-code-marketplace\n\n2. After installation, use /pr-comments:pr-comments to run this command\n\n3. For more information, see: https://github.com/anthropics/claude-code-marketplace/blob/main/pr-comments/README.md\n\nDo not attempt to run the command. Simply inform the user about the plugin installation.", ctx.Err()
		}
		additional := ""
		if value := argumentString(args); value != "" {
			additional = "Additional user input: " + value
		}
		return strings.Replace(prCommentsPrompt, "${args ? 'Additional user input: ' + args : ''}", additional, 1), ctx.Err()
	})
}
