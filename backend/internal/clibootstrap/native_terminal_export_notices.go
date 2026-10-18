package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ideintegration"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (m *MainPrint) terminalExportStatusContext(ctx context.Context, scope *cooperative.Scope, global *protocol.JSONObject) (tui.ExportStatusContext, error) {
	value := tui.ExportStatusContext{CWD: m.workingDirectory(), AutoInstallIDE: true}
	identity, tokens, _, enabled, err := m.terminalStatusAuth(ctx, scope)
	if err != nil {
		return value, err
	}
	value.Subscriber, err = openaiauth.ClaudeSubscriber(enabled, tokens)
	if err != nil {
		return value, err
	}
	value.AuthTokenSource, value.APIKeySource = identity.AuthTokenSource, identity.APIKeySource
	owned, err := m.openAIAuth.owned(ctx, scope)
	if err != nil {
		return value, err
	}
	if err = m.openAIAuth.ensureLegacyAPIKeyCached(owned); err != nil {
		return value, err
	}
	value.StoredAPIKey = m.openAIAuth.legacyAPIKeyPresent
	future, err := nativeSkillWork(m, scope, m.userContext.MemoryFilesForInspection)
	if err != nil {
		return value, err
	}
	files, err := future.Await(scope)
	if err != nil {
		return value, err
	}
	for _, file := range files {
		units, err := protocol.DecodeJavaScriptJSONString(file.PromptContent())
		if err != nil {
			return value, err
		}
		value.Memory = append(value.Memory, tui.ExportMemoryNotice{Path: file.Path, Characters: len(units)})
	}
	// renderMessagesToPlainText's original Messages caller omits definitions.
	// Loading all active agent definitions here would change its notice gate.
	value.AgentDescriptionTokens = 0
	if global != nil && string(global.Get("autoInstallIdeExtension")) == "false" {
		value.AutoInstallIDE = false
	}
	environment := m.nativeOpenAIEnvironment()
	kind := ideintegration.Terminal(environment)
	if ideintegration.JetBrainsIDE(kind) && ideintegration.SupportedTerminal(kind, environment) {
		value.JetBrainsIDE = ideintegration.IDEName(kind)
		if m.ideIntegration != nil {
			future, err := nativeSkillWork(m, scope, func() (bool, error) { return m.ideIntegration.extensions.Installed(ctx, kind), nil })
			if err != nil {
				return value, err
			}
			value.JetBrainsInstalled, err = future.Await(scope)
			if err != nil {
				return value, err
			}
		}
	}
	return value, nil
}
