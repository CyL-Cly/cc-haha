package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/authhelper"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func (m *MainPrint) nativeConfiguredAPIKeyHelper() (string, error) {
	raw, err := m.nativeConfiguredAPIKeyHelperJSON()
	if err != nil || raw == nil {
		return "", err
	}
	var command string
	err = json.Unmarshal(raw, &command)
	return command, err
}
func (m *MainPrint) nativeConfiguredAPIKeyHelperJSON() (json.RawMessage, error) {
	var settings *protocol.JSONObject
	var err error
	if envTruthyNative(m.nativeOpenAIEnvironment()["CLAUDE_CODE_SIMPLE"]) {
		settings, err = m.Startup.GetSettingsForSource("flagSettings")
	} else {
		settings, err = m.nativeSDKInitialSettings()
	}
	if err != nil || settings == nil {
		return nil, err
	}
	value := settings.Get("apiKeyHelper")
	if !planRawTruthy(value) {
		return nil, nil
	}
	if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
		return nil, mainCap("apiKeyHelper original string command")
	}
	return value, nil
}
func (m *MainPrint) initializeNativeAPIKeyHelper() {
	if !m.options.Native.NativeProcess {
		return
	}
	if parent := m.nativeHelperParent(); parent != nil {
		m.apiKeyHelper = parent.apiKeyHelper
		m.apiKeyHelperBorrowed = true
		return
	}
	m.apiKeyHelper = authhelper.New(authhelper.Options{NowMillis: m.options.Native.NowMillis,
		Dispatch: func(settle func()) error {
			receipt, err := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error { settle(); return nil })
			if err != nil {
				return err
			}
			return receipt.Wait()
		},
		Error: func(detail string) { _, _ = fmt.Fprintln(m.options.Error, "apiKeyHelper failed: "+detail) },
	})

}
func (m *MainPrint) nativeAPIKeyHelperRequest(scope *cooperative.Scope, nonInteractive bool) (authhelper.Request, error) {
	if !scope.BelongsTo(m.Owner) || m.apiKeyHelper == nil {
		return authhelper.Request{}, cooperative.ErrRestricted
	}
	command, err := m.nativeConfiguredAPIKeyHelper()
	if err != nil || command == "" {
		return authhelper.Request{}, err
	}
	env := m.nativeOpenAIEnvironment()
	commandJSON, err := m.nativeConfiguredAPIKeyHelperJSON()
	if err != nil {
		return authhelper.Request{}, err
	}
	fromProject := false
	for _, source := range []string{"projectSettings", "localSettings"} {
		settings, err := m.Startup.GetSettingsForSource(source)
		if err != nil {
			return authhelper.Request{}, err
		}
		if settings != nil {
			if authhelper.ConfiguredInSource(commandJSON, settings.Get("apiKeyHelper")) {
				fromProject = true
			}
		}
	}
	trusted := false
	if fromProject && !nonInteractive {
		trusted, err = m.nativeGoalTrust(scope)
		if err != nil {
			return authhelper.Request{}, err
		}
	}
	cwd := m.state.Snapshot().CWD
	return authhelper.Request{Command: command, ProjectOrLocal: fromProject, TrustAccepted: trusted, NonInteractive: nonInteractive, TTL: env["CLAUDE_CODE_API_KEY_HELPER_TTL_MS"], Execute: func(ctx context.Context, command string) (string, error) {
		return authhelper.Execute(ctx, command, authhelper.ProcessOptions{CWD: cwd, Environment: env})
	}}, nil
}
func (m *MainPrint) nativeAuthHelperBinding(scope *cooperative.Scope) (*requestretry.NativeAuthHelper, error) {
	if m.apiKeyHelper == nil {
		return nil, nil
	}
	if parent := m.nativeHelperParent(); parent != nil {
		return requestretry.NewNativeAuthHelper(m.Owner, scope, func(ctx context.Context, _ bool) (requestretry.NativeAuthHelperResult, error) {
			return nativeAgentOwnerCall(m, scope, parent, func(parentScope *cooperative.Scope) (requestretry.NativeAuthHelperResult, error) {
				binding, err := parent.nativeAuthHelperBinding(parentScope)
				if err != nil {
					return requestretry.NativeAuthHelperResult{}, err
				}
				return binding.Resolve(ctx, !parent.options.Interactive)
			})
		}, func() error {
			return bridgeAgentVoid(m, parent, scope, func(parentScope *cooperative.Scope) error {
				binding, err := parent.nativeAuthHelperBinding(parentScope)
				if err != nil {
					return err
				}
				return binding.Invalidate()
			})
		})
	}
	return requestretry.NewNativeAuthHelper(m.Owner, scope, func(ctx context.Context, nonInteractive bool) (requestretry.NativeAuthHelperResult, error) {
		nonInteractive = !m.options.Interactive
		env := m.nativeOpenAIEnvironment()
		command, err := m.nativeConfiguredAPIKeyHelper()
		if err != nil {
			return requestretry.NativeAuthHelperResult{}, err
		}
		result := requestretry.NativeAuthHelperResult{Configured: command != ""}
		var token *string
		if value := env["ANTHROPIC_AUTH_TOKEN"]; value != "" {
			token = &value
		} else {
			request, err := m.nativeAPIKeyHelperRequest(scope, nonInteractive)
			if err != nil {
				return result, err
			}
			if request.Command != "" {
				lookup, err := m.apiKeyHelper.Begin(request)
				if err != nil {
					return result, err
				}
				future, err := nativeSkillWork(m, scope, func() (*string, error) { return lookup.Wait(context.Background()) })
				if err != nil {
					return result, err
				}
				token, err = future.Await(scope)
				if err != nil {
					return result, err
				}
			}
		}
		value, source, err := m.openAIAuth.apiKeyWithSource(context.WithoutCancel(ctx), env, envTruthyNative(env["CLAUDE_CODE_SIMPLE"]), nonInteractive)
		if err != nil {
			return result, err
		}
		var key *string
		if source == "apiKeyHelper" {
			key = m.apiKeyHelper.Cached()
		} else if source != "none" {
			key = &value
		}
		result.Key, result.Token = key, token
		return result, nil
	}, func() error { m.apiKeyHelper.Clear(); return nil })
}

func (m *MainPrint) nativeHelperParent() *MainPrint {
	if d := m.options.DetachedAgent; d != nil {
		if d.authParent != nil {
			return d.authParent
		}
		if d.preset != nil {
			return d.preset.parent
		}
	}
	return nil
}
