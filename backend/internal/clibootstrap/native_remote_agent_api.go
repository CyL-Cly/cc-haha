package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/ccrsessions"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
)

// Task restoration does not pass through --remote's eligibility/title/model
// producers. The original fetch/poll task API consumes the cached account only.
func (m *MainPrint) nativeRemoteAgentAPI(scope *cooperative.Scope) (*ccrsessions.TaskAPI, error) {
	if m == nil || scope == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || m.openAIAuth == nil || m.closed || m.shuttingDown {
		return nil, cooperative.ErrRestricted
	}
	env := m.nativeOpenAIEnvironment()
	config, err := desktopoauth.ResolveConfig(env)
	if err != nil {
		return nil, err
	}
	client, err := m.nativeRemoteHTTP(env)
	if err != nil {
		return nil, err
	}
	return ccrsessions.NewTaskAPI(ccrsessions.TaskAPIOptions{BaseURL: config.BaseAPIURL, HTTP: client, Credentials: func(ctx context.Context, refresh bool) (ccrsessions.Credentials, error) {
		// The task API never refreshes OAuth. A true refresh request cannot borrow
		// this cached-account authority from the distinct creation/archive API.
		if refresh {
			return ccrsessions.Credentials{}, cooperative.ErrRestricted
		}
		var value ccrsessions.Credentials
		receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(next *cooperative.Scope) error {
			if m.closed || m.shuttingDown {
				return cooperative.ErrClosed
			}
			owned, err := m.openAIAuth.owned(ctx, next)
			if err != nil {
				return err
			}
			current := m.nativeOpenAIEnvironment()
			tokens, err := m.openAIAuth.claudeTokensFor(owned, current, pickerTrue(current["CLAUDE_CODE_SIMPLE"]))
			if err != nil {
				return err
			}
			if tokens != nil {
				value.Token = tokens.Object.String("accessToken")
				_ = json.Unmarshal(tokens.Object.Get("scopes"), &value.Scopes)
			}
			global, err := m.Global.Get()
			if err != nil {
				return err
			}
			if global != nil {
				account, _ := global.Object("oauthAccount")
				value.Organization = account.String("organizationUuid")
			}
			return nil
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			return value, err
		}
		if value.Organization == "" && value.Token != "" {
			for _, scope := range value.Scopes {
				if scope == "user:profile" {
					profile := (&cliauth.OAuthClient{Config: config, HTTP: client}).Profile(ctx, value.Token)
					if profile != nil {
						organization, _ := profile.Object("organization")
						value.Organization = organization.String("uuid")
					}
					break
				}
			}
		}
		return value, nil
	}})
}
