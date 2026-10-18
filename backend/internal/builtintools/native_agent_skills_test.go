package builtintools

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func TestAgentSkillPreloadStillRejectsFiniteCallbacksWithoutActualNativeParentGraph(t *testing.T) {
	s, owner, _, _ := fixture(t)
	s.options.RunAgent = func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error) {
		t.Error("finite callback acquired actual native preset lifecycle")
		return nil, nil
	}
	s.options.AgentDefinitions = func(*cooperative.Scope) ([]agentcatalog.Definition, error) {
		return []agentcatalog.Definition{{Name: "configured", SystemPrompt: "controlled definition", Fields: input(`{"tools":["Read"],"skills":["late-skill"]}`)}}, nil
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := s.BindNativeAgentSkills(scope, new(sessioncontroller.Controller), nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("zero/finite Controller manufactured preload proof", err)
		}
		_, err := s.startAgent(context.Background(), input(`{"subagent_type":"configured","prompt":"read"}`), queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}, ToolUseID: quote("finite-preload")})
		var capability *CapabilityError
		if !errors.As(err, &capability) || capability.Boundary != "Agent configured skill preload lifecycle" {
			t.Error("finite configured skill capability was weakened", err)
		}
		if err := s.BindNativeAgentMCP(scope, new(sessioncontroller.Controller), nil, nil, nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("finite Controller manufactured MCP preload proof", err)
		}
		s.options.AgentDefinitions = func(*cooperative.Scope) ([]agentcatalog.Definition, error) {
			return []agentcatalog.Definition{{Name: "configured", SystemPrompt: "controlled definition", Fields: input(`{"tools":["Read"],"mcpServers":[{"private":{"type":"http","url":"http://127.0.0.1:1"}}]}`)}}, nil
		}
		_, err = s.startAgent(context.Background(), input(`{"subagent_type":"configured","prompt":"read"}`), queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}, ToolUseID: quote("finite-mcp-preload")})
		if !errors.As(err, &capability) || capability.Boundary != "Agent configured mcpServers lifecycle" {
			t.Error("finite configured MCP capability was weakened", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := s.BindNativeAgentSkills(scope, nil, nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign callback installed preload proof", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
