package skillcommands

import (
	"context"
	_ "embed"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Authored Source prompts retain original text; no frontmatter/shell or user
// arguments are substituted into this built-in command.
//
//go:embed assets/builtin/init-old.md
var oldInitPrompt string

//go:embed assets/builtin/init-new.md
var newInitPrompt string

type InitCommandOptions struct {
	NewInit                            bool
	Environment                        func(Invocation, string) (string, error)
	MaybeMarkProjectOnboardingComplete func(context.Context, Invocation) error
}

func initNewPrompt(o InitCommandOptions, invocation Invocation) (bool, error) {
	if !o.NewInit {
		return false, nil
	}
	userType, err := o.Environment(invocation, "USER_TYPE")
	if err != nil || userType == "ant" {
		return userType == "ant", err
	}
	flag, err := o.Environment(invocation, "CLAUDE_CODE_NEW_INIT")
	return initTruthy(flag), err
}

func initTruthy(value string) bool {
	switch strings.ToLower(trimJS(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func InitCommandMetadata(newInit bool) protocol.JSONObject {
	metadata := protocol.NewJSONObject()
	set(metadata, "type", "prompt")
	set(metadata, "name", "init")
	description := "Initialize a new CLAUDE.md file with codebase documentation"
	if newInit {
		description = "Initialize new CLAUDE.md file(s) and optional skills/hooks with codebase documentation"
	}
	set(metadata, "description", description)
	set(metadata, "contentLength", 0)
	set(metadata, "progressMessage", "analyzing your codebase")
	set(metadata, "source", "builtin")
	return metadata
}

func NativeInitCommand(o InitCommandOptions) (Definition, error) {
	if o.Environment == nil || o.MaybeMarkProjectOnboardingComplete == nil {
		return Definition{}, errors.New("init requires actual environment and original project onboarding producer")
	}
	// Source's CLI build feature set is pinned by cliInventory.ts. The normal
	// distribution omits NEW_INIT; alternate feature profiles pass it explicitly.
	useNew, err := initNewPrompt(o, Invocation{})
	if err != nil {
		return Definition{}, err
	}
	return NewNativeCommand(InitCommandMetadata(useNew), func(ctx context.Context, _ *string, invocation Invocation) (string, error) {
		if err := o.MaybeMarkProjectOnboardingComplete(ctx, invocation); err != nil {
			return "", err
		}
		useNew, err := initNewPrompt(o, invocation)
		if err != nil {
			return "", err
		}
		if useNew {
			return newInitPrompt, ctx.Err()
		}
		return oldInitPrompt, ctx.Err()
	})
}
