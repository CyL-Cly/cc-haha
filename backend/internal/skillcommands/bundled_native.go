package skillcommands

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NewBundled binds a shipped declaration's actual command producer. Bundled
// commands do not parse frontmatter, substitute disk-skill args or run a shell.
func NewBundled(metadata protocol.JSONObject, body string, prompt func(context.Context, *string) (string, error)) (Definition, error) {
	if !metadata.IsObject() || metadata.String("type") != "prompt" || metadata.String("source") != "bundled" || metadata.String("loadedFrom") != "bundled" || metadata.String("name") == "" || prompt == nil {
		return Definition{}, errors.New("bundled skill requires its original declaration and command producer")
	}
	return Definition{metadata: metadata.Clone(), body: body, name: metadata.String("name"), loadedFrom: "bundled", bundledPrompt: prompt}, nil
}
