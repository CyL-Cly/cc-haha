package builtintools

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

type nativeAgentSkillPreload struct {
	controller *sessioncontroller.Controller
	commands   *skillcommands.Service
	foreground func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error)
	background func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error)
}

// BindNativeAgentSkills is called only at the actual MainPrint factory, after
// the canonical runner and real process command/Controller graph exist. The
// concrete Writer/profile proof is additional to callback availability; no
// environment flag or JSON field can manufacture it. Keep the admitted runner
// itself so replacing an Options callback cannot borrow its preload authority.
func (s *Service) BindNativeAgentSkills(scope *cooperative.Scope, controller *sessioncontroller.Controller, commands *skillcommands.Service) error {
	if err := s.check(scope); err != nil {
		return err
	}
	if s.nativeAgentSkills != nil || commands == nil || !controller.NativeAgentSkillPreloadProfile(scope, s.options.Owner, s.options.NativeBinding) || s.options.RunAgent == nil || s.options.AgentDefinitions == nil || s.options.AgentPromptJSON == nil || !s.HasSkillBindings() {
		return cooperative.ErrRestricted
	}
	s.nativeAgentSkills = &nativeAgentSkillPreload{controller, commands, s.options.RunAgent, s.options.RunBackgroundAgent}
	return nil
}

func (s *Service) nativeAgentSkillRunner(scope *cooperative.Scope, background bool) (func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error), bool) {
	binding := s.nativeAgentSkills
	if binding == nil || binding.commands == nil || !binding.controller.NativeAgentSkillPreloadProfile(scope, s.options.Owner, s.options.NativeBinding) {
		return nil, false
	}
	runner := binding.foreground
	if background {
		runner = binding.background
	}
	return runner, runner != nil
}
