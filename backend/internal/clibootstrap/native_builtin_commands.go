package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func (m *MainPrint) nativeBuiltinPromptCommands() ([]skillcommands.Definition, error) {
	agent, err := skillcommands.NativeAgentCommand()
	if err != nil {
		return nil, err
	}
	environment := func(invocation skillcommands.Invocation, key string) (string, error) {
		if !invocation.NativeScope.BelongsTo(m.Owner) || m.options.Native == nil {
			return "", cooperative.ErrRestricted
		}
		return m.options.Native.Environment[key], nil
	}
	prComments, err := skillcommands.NativePRCommentsCommand(environment)
	if err != nil {
		return nil, err
	}
	// The original distribution's cliBuildFeatures is TRANSCRIPT_CLASSIFIER;
	// NEW_INIT is omitted, including when its user environment flag is set.
	init, err := skillcommands.NativeInitCommand(skillcommands.InitCommandOptions{NewInit: false, Environment: environment, MaybeMarkProjectOnboardingComplete: m.nativeMaybeMarkProjectOnboardingComplete})
	if err != nil {
		return nil, err
	}
	securityReview, err := skillcommands.NativeSecurityReviewCommand(environment, func(invocation skillcommands.Invocation) (string, error) {
		if !invocation.NativeScope.BelongsTo(m.Owner) {
			return "", cooperative.ErrRestricted
		}
		// Original getShellToolAvailability always enables Bash on macOS/Linux,
		// including when a persisted PowerShell preference is present.
		if m.options.Platform == "windows" {
			return "", mainCap("security review Windows Git Bash/PowerShell availability")
		}
		return "bash", nil
	})
	if err != nil {
		return nil, err
	}
	insights, err := m.nativeInsightsCommand()
	if err != nil {
		return nil, err
	}
	return []skillcommands.Definition{agent, init, prComments, securityReview, insights}, nil
}
