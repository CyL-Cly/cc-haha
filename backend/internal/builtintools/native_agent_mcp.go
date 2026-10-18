package builtintools

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcptools"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

type nativeAgentMCPPreset struct {
	controller     *sessioncontroller.Controller
	tools          *mcptools.Service
	runtime        *mcpruntime.Service
	configurations *mcpconfig.Service
	foreground     func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error)
	background     func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error)
}

// BindNativeAgentMCP is separate from skill availability: only the canonical
// native Writer plus its actual MCP runtime/configuration graph and original
// child runners can consume an agent's additional server declarations.
func (s *Service) BindNativeAgentMCP(scope *cooperative.Scope, controller *sessioncontroller.Controller, tools *mcptools.Service, runtime *mcpruntime.Service, configurations *mcpconfig.Service) error {
	if err := s.check(scope); err != nil {
		return err
	}
	if s.nativeAgentMCP != nil || !controller.NativeAgentSkillPreloadProfile(scope, s.options.Owner, s.options.NativeBinding) || !tools.NativeAgentPresetProfile(scope, s.options.Owner, s.options.NativeBinding, runtime, configurations) || s.options.RunAgent == nil || s.options.AgentDefinitions == nil || s.options.AgentPromptJSON == nil {
		return cooperative.ErrRestricted
	}
	s.nativeAgentMCP = &nativeAgentMCPPreset{controller, tools, runtime, configurations, s.options.RunAgent, s.options.RunBackgroundAgent}
	return nil
}

func (s *Service) nativeAgentMCPRunner(scope *cooperative.Scope, background bool) (func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error), bool) {
	b := s.nativeAgentMCP
	if b == nil || !b.controller.NativeAgentSkillPreloadProfile(scope, s.options.Owner, s.options.NativeBinding) || !b.tools.NativeAgentPresetProfile(scope, s.options.Owner, s.options.NativeBinding, b.runtime, b.configurations) {
		return nil, false
	}
	runner := b.foreground
	if background {
		runner = b.background
	}
	return runner, runner != nil
}
