package clibootstrap

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/ccrsessions"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

//go:embed native_remote_title_prompt.md
var nativeRemoteTitlePrompt string

func (m *MainPrint) GenerateCLIRemoteTitle(ctx context.Context, description string) (ccrsessions.Title, error) {
	var text string
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if m.Host == nil || m.nativeModelSelector == nil {
			return cooperative.ErrRestricted
		}
		var err error
		text, err = m.Host.QueryRemoteTitle(ctx, scope, quoteMain(strings.Replace(nativeRemoteTitlePrompt, "{description}", description, 1)), m.nativeModelSelector.SmallFastModel())
		return err
	})
	if err != nil {
		return ccrsessions.Title{}, err
	}
	var result protocol.JSONObject
	if result.UnmarshalJSON([]byte(strings.TrimSpace(text))) != nil {
		return ccrsessions.Title{}, errors.New("Invalid remote session title JSON")
	}
	var title, branch string
	if result.Get("title") == nil || result.Get("branch") == nil || json.Unmarshal(result.Get("title"), &title) != nil || json.Unmarshal(result.Get("branch"), &branch) != nil || string(result.Get("title")) == "null" || string(result.Get("branch")) == "null" {
		return ccrsessions.Title{}, errors.New("Invalid remote session title fields")
	}
	return ccrsessions.Title{Title: title, Branch: branch}, nil
}

func (m *MainPrint) NewCLICCRSessions(ctx context.Context) (*ccrsessions.Service, error) {
	var env map[string]string
	var model string
	var sid string
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		env = m.nativeOpenAIEnvironment()
		if m.nativeModelSelector == nil {
			return cooperative.ErrRestricted
		}
		raw, err := m.nativeModelSelector.Select()
		if err != nil {
			return err
		}
		model, err = decodeNativeModel(raw)
		sid = m.Controller.SessionID()
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		return nil, err
	}
	config, err := desktopoauth.ResolveConfig(env)
	if err != nil {
		return nil, err
	}
	client, err := m.nativeRemoteHTTP(env)
	if err != nil {
		return nil, err
	}
	credential := func(ctx context.Context, refresh bool) (ccrsessions.Credentials, error) {
		if refresh && m.claudeAIMCP != nil && m.claudeAIMCP.refresh != nil {
			if err := m.claudeAIMCP.refresh.check(ctx); err != nil {
				return ccrsessions.Credentials{}, err
			}
		}
		value := ccrsessions.Credentials{}
		read, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			owned, err := m.openAIAuth.owned(ctx, scope)
			if err != nil {
				return err
			}
			current := m.nativeOpenAIEnvironment()
			tokens, err := m.openAIAuth.claudeTokensFor(owned, current, pickerTrue(current["CLAUDE_CODE_SIMPLE"]))
			if err != nil {
				return err
			}
			if tokens != nil {
				value.Token, value.Subscription = tokens.Object.String("accessToken"), tokens.Object.String("subscriptionType")
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
			source, err := m.nativeAPIKeySource(scope)
			if err != nil {
				return err
			}
			if source == "ANTHROPIC_API_KEY" {
				value.APIKey = current["ANTHROPIC_API_KEY"]
			} else if source == "/login managed key" {
				value.APIKey = m.openAIAuth.legacyAPIKey
			}
			return nil
		})
		if err == nil {
			err = read.Wait()
		}
		if err != nil {
			return value, err
		}
		if value.Organization == "" && value.Token != "" {
			profileScope := false
			for _, scope := range value.Scopes {
				profileScope = profileScope || scope == "user:profile"
			}
			if profileScope {
				profile := (&cliauth.OAuthClient{Config: config, HTTP: client}).Profile(ctx, value.Token)
				if profile != nil {
					organization, _ := profile.Object("organization")
					value.Organization = organization.String("uuid")
				}
			}
		}
		return value, nil
	}
	n := m.options.Native
	configDir := n.ConfigHomeDirectory()
	if configDir == "" {
		configDir = filepath.Join(n.Home, ".claude")
	} else if !filepath.IsAbs(configDir) {
		configDir = filepath.Join(n.CWD, configDir)
	}
	env["CC_HAHA_VERSION"] = provider.SourceCompatibilityVersion
	remoteBackend, err := m.NativeCLIRemoteBackendEnabled(ctx)
	if err != nil {
		return nil, err
	}
	return ccrsessions.New(ccrsessions.Options{RemoteBackendEnabled: remoteBackend, BaseURL: config.BaseAPIURL, CWD: n.CWD, ConfigDir: configDir, TempDir: n.Temp, SessionID: sid, Model: model, Environment: env, HTTP: client, Credentials: credential, GenerateTitle: m.GenerateCLIRemoteTitle, GenerateSessionTitle: m.GenerateCLISessionTitle, Settings: func(context.Context) (protocol.JSONObject, error) {
		value, err := m.nativeSettings.InitialSettings()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}, Report: func(err error) { _ = m.nativeLogError(context.Background(), err) }})
}
