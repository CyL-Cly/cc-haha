package queryhooks

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

type AgentModelInput struct {
	Hook                                             protocol.JSONObject
	Prompt                                           protocol.JSONObject
	HookAgentID, Model, SystemPrompt, TranscriptPath string
	ToolUseContext                                   any
}
type AgentBindings struct {
	Owner      *cooperative.Owner
	Transcript func(*cooperative.Scope, Context) (string, error)
	Model      func(*cooperative.Scope, protocol.JSONObject) (string, error)
	Query      func(context.Context, *cooperative.Scope, AgentModelInput, func(queryloop.Message) (bool, error)) error
	Clear      func(*cooperative.Scope, string) error
}
