package clibootstrap

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
)

func terminalMCPEncodeURIComponent(value string) string {
	return strings.NewReplacer("+", "%20", "%21", "!", "%27", "'", "%28", "(", "%29", ")", "%2A", "*", "%7E", "~").Replace(url.QueryEscape(value))
}

// This prepares the Source connector browser URL only. It performs no account
// login/refresh. Enter later consumes the existing reconnect/cache producer.
func (m *MainPrint) TerminalMCPClaudeAIAuthURL(ctx context.Context, name string, clear bool) (string, error) {
	if m == nil || m.Owner == nil || ctx == nil {
		return "", cooperative.ErrRestricted
	}
	var address string
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		config, err := m.terminalMCPRemoteTarget(ctx, scope, name)
		if err != nil {
			return err
		}
		if config.String("type") != "claudeai-proxy" {
			return errors.New("Server is not a Claude AI connector: " + name)
		}
		env := m.nativeOpenAIEnvironment()
		origin, err := desktopoauth.ClaudeAIOrigin(env)
		if err != nil {
			return err
		}
		address = origin + "/settings/connectors"
		if clear {
			return nil
		}
		owned, err := m.openAIAuth.owned(ctx, scope)
		if err != nil {
			return err
		}
		bare := nativeEnvTruthy(env["CLAUDE_CODE_SIMPLE"])
		settings, err := m.openAIAuth.authDependencies(env, bare)
		if err != nil {
			return err
		}
		keySource, err := m.openAIAuth.apiKeySource(owned, env, bare, false)
		if err != nil {
			return err
		}
		if !openaiauth.AnthropicAuthEnabled(openaiauth.AnthropicAuthInput{Bare: bare, Environment: env, APIKeySource: keySource, APIKeyHelper: settings.Get("apiKeyHelper")}) {
			return nil
		}
		global, err := m.Global.Get()
		if err != nil || global == nil {
			return err
		}
		account, _ := global.Object("oauthAccount")
		org, serverID := account.String("organizationUuid"), config.String("id")
		if org == "" || serverID == "" {
			return nil
		}
		if strings.HasPrefix(serverID, "mcprs") {
			serverID = "mcpsrv" + strings.TrimPrefix(serverID, "mcprs")
		}
		surface := env["CLAUDE_CODE_ENTRYPOINT"]
		if surface == "" {
			surface = "cli"
		}
		address = origin + "/api/organizations/" + org + "/mcp/start-auth/" + serverID + "?product_surface=" + terminalMCPEncodeURIComponent(surface)
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return address, err
}

func (m *MainPrint) CompleteTerminalMCPClaudeAIAuthentication(ctx context.Context, name string, clear bool) error {
	if m == nil || m.Owner == nil || ctx == nil {
		return cooperative.ErrRestricted
	}
	ctx, release := m.nativeTerminalMCPAuthLife.bind(ctx)
	defer release()
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		config, err := m.terminalMCPRemoteTarget(ctx, scope, name)
		if err != nil {
			return err
		}
		if config.String("type") != "claudeai-proxy" {
			return errors.New("Server is not a Claude AI connector: " + name)
		}
		if clear {
			err = m.mcpTools.ClearTerminalClaudeAIAuthentication(ctx, scope, name, config)
		} else {
			err = m.mcpTools.ReconnectTerminal(ctx, scope, name)
		}
		if err != nil {
			return err
		}
		return m.publishNativeMCPCatalog(scope)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
