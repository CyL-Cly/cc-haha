package skillcommands

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NewNativeCommand adapts an actual original prompt command producer. It adds
// no frontmatter, skill source marker, shell substitution or permission grant.
func NewNativeCommand(metadata protocol.JSONObject, prompt func(context.Context, *string, Invocation) (string, error)) (Definition, error) {
	if !metadata.IsObject() || metadata.String("type") != "prompt" || metadata.String("name") == "" || prompt == nil {
		return Definition{}, errors.New("native prompt command requires its actual declaration/producer")
	}
	return Definition{metadata: metadata.Clone(), name: metadata.String("name"), nativePrompt: prompt}, nil
}

func NewNativeContentCommand(metadata protocol.JSONObject, prompt func(context.Context, *string, Invocation) ([]protocol.JSONObject, error)) (Definition, error) {
	if !metadata.IsObject() || metadata.String("type") != "prompt" || metadata.String("name") == "" || prompt == nil {
		return Definition{}, errors.New("native content command requires its actual declaration/producer")
	}
	return Definition{metadata: metadata.Clone(), name: metadata.String("name"), nativeContent: prompt}, nil
}
