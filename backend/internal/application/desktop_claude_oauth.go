package application

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
)

func desktopOAuthBaseEnvironment(o Options, base map[string]string) map[string]string {
	if o.DesktopOAuthToken == nil || o.desktopTeamLaunch == nil {
		return base
	}
	env := make(map[string]string, len(base))
	for key, value := range base {
		env[key] = value
	}
	delete(env, "CLAUDE_CODE_OAUTH_TOKEN")
	delete(env, "CLAUDE_CODE_ACCOUNT_UUID")
	// Runtime settings pass an explicit null or saved-provider identity to the
	// desktop child producer. Strip the inherited managed keys before projection.
	if o.desktopManagedClaudeOAuth && o.RuntimeSelection != nil {
		for _, key := range providerconfig.ManagedKeys() {
			delete(env, key)
		}
	}
	return env
}

func desktopOAuthEnvironment(ctx context.Context, o Options, base map[string]string) map[string]string {
	if o.DesktopOAuthToken == nil || !o.desktopManagedClaudeOAuth || o.desktopTeamLaunch == nil || o.RuntimeSelection != nil && o.RuntimeSelection.ProviderID != nil {
		return base
	}
	env := make(map[string]string, len(base)+2)
	for key, value := range base {
		env[key] = value
	}
	env["CLAUDE_CODE_ENTRYPOINT"] = "claude-desktop"
	token, err := o.DesktopOAuthToken(ctx)
	if err != nil {
		fmt.Fprintf(o.Stderr, "[conversationService] ensureFreshAccessToken failed: %s\n", err)
	} else if token != nil && *token != "" {
		env["CLAUDE_CODE_OAUTH_TOKEN"] = *token
		if o.DesktopOAuthAccountUUID != nil {
			account, lookupError := o.DesktopOAuthAccountUUID(ctx, *token)
			if lookupError == nil && account != nil && *account != "" {
				env["CLAUDE_CODE_ACCOUNT_UUID"] = *account
			}
		}
	}
	return env
}

// The caller holds the session's serial turn admission. HTTP refresh runs
// before the owner starts ASK; delivery is the existing owner's Input command.
func (s *Session) refreshDesktopOAuth(ctx context.Context) error {
	if s.desktopOAuthToken == nil {
		return nil
	}
	token, err := s.desktopOAuthToken(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		fmt.Fprintf(s.desktopOAuthError, "[conversationService] refresh official OAuth token before turn failed: %s\n", err)
		return nil
	}
	if token == nil || *token == "" || *token == s.desktopOfficialToken {
		return nil
	}
	if err := s.process.SetDesktopOAuthToken(ctx, *token); err != nil {
		return err
	}
	s.desktopOfficialToken = *token
	return nil
}
