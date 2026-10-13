package queryhooks

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type PromptModelInput struct {
	ToolNames *[]string
	Hook      protocol.JSONObject
	History   []protocol.JSONObject
	Prompt    json.RawMessage
}
type PromptBindings struct {
	Owner              *cooperative.Owner
	Query              func(context.Context, *cooperative.Scope, PromptModelInput) (protocol.JSONObject, error)
	RestoreGoal        func(context.Context, Context) (bool, error)
	TeamWorkInProgress func(context.Context, Context) (bool, error)
}

func (s *Service) HasNativePromptBindings(owner *cooperative.Owner) bool {
	return s != nil && s.options.Prompts != nil && s.options.Prompts.Owner == owner && s.options.Prompts.Query != nil && s.HasNativeCommandBindings(owner)
}
