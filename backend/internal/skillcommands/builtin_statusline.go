package skillcommands

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func StatuslineCommandMetadata() protocol.JSONObject {
	metadata := protocol.NewJSONObject()
	set(metadata, "type", "prompt")
	set(metadata, "description", "Set up Claude Code's status line UI")
	set(metadata, "contentLength", 0)
	set(metadata, "aliases", []string{})
	set(metadata, "name", "statusline")
	set(metadata, "progressMessage", "setting up statusLine")
	set(metadata, "allowedTools", []string{"Agent", "Read(~/**)", "Edit(~/.claude/settings.json)"})
	set(metadata, "source", "builtin")
	set(metadata, "disableNonInteractive", true)
	return metadata
}

func NativeStatuslineCommand() (Definition, error) {
	return NewNativeCommand(StatuslineCommandMetadata(), func(ctx context.Context, args *string, _ Invocation) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		prompt := trimJS(argumentString(args))
		if prompt == "" {
			prompt = "Configure my statusLine from my shell PS1 configuration"
		}
		return "Create an Agent with subagent_type \"statusline-setup\" and the prompt \"" + prompt + "\"", nil
	})
}
