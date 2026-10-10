package skillcommands

import (
	"context"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// MalformedCommandError is an original prompt command's user-facing usage
// error. The slash consumer records it as local input without querying a model.
type MalformedCommandError struct{ Message string }

func (e *MalformedCommandError) Error() string { return e.Message }

type AgentCommandArgs struct {
	AgentType string `json:"agentType"`
	Prompt    string `json:"prompt"`
}

func ParseAgentCommandArgs(args string) *AgentCommandArgs {
	trimmed := trimJS(args)
	if trimmed == "" {
		return nil
	}
	firstSpace := strings.IndexFunc(trimmed, func(r rune) bool { return trimJS(string(r)) == "" })
	if firstSpace < 0 {
		return nil
	}
	prompt := trimJS(trimmed[firstSpace:])
	if prompt == "" {
		return nil
	}
	return &AgentCommandArgs{AgentType: trimmed[:firstSpace], Prompt: prompt}
}

// AgentCommandMetadata is the shipped /agent declaration. Both the executable
// inventory and cold desktop metadata use this authored producer.
func AgentCommandMetadata() protocol.JSONObject {
	metadata := protocol.NewJSONObject()
	set(metadata, "type", "prompt")
	set(metadata, "name", "agent")
	set(metadata, "description", "Run a prompt with a selected Agent")
	set(metadata, "argumentHint", "<agent> <prompt>")
	set(metadata, "progressMessage", "running agent")
	set(metadata, "contentLength", 0)
	set(metadata, "source", "builtin")
	set(metadata, "allowedTools", []string{"Agent"})
	return metadata
}

func NativeAgentCommand() (Definition, error) {
	return NewNativeCommand(AgentCommandMetadata(), func(ctx context.Context, args *string, _ Invocation) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		parsed := ParseAgentCommandArgs(argumentString(args))
		if parsed == nil {
			return "", &MalformedCommandError{"Usage: /agent <agent> <prompt>"}
		}
		return "Use the Agent tool with subagent_type \"" + parsed.AgentType + "\" to handle this request.\nPass this exact prompt to that agent:\n\n" + parsed.Prompt, nil
	})
}
