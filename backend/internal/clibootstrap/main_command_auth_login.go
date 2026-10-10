package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type AuthLoginOptions struct {
	Input   cliauth.LoginInput
	Version string
	// Explicit external-transport/browser primitives. The ordinary public
	// consumer supplies nil and uses original endpoint configuration/OS opener.
	HTTP    *http.Client
	Config  *desktopoauth.Config
	Browser func(context.Context, string) error
}

func (m *MainCommand) AuthLogin(ctx context.Context, scope *cooperative.Scope, o AuthLoginOptions) (cliauth.Result, error) {
	if !scope.BelongsTo(m.Owner) || !m.Native.NativeProcess {
		return cliauth.Result{}, cooperative.ErrRestricted
	}
	config, e := desktopoauth.ResolveConfig(m.Native.Environment)
	if e != nil {
		return cliauth.Result{}, e
	}
	if o.Config != nil {
		config = *o.Config
	}
	tls, e := tlsconfig.NewState(m.Native.readFile)
	if e != nil {
		return cliauth.Result{}, e
	}
	client := o.HTTP
	if client == nil {
		client, e = webfetch.NewHTTPClient(m.Native.Environment, tls)
		if e != nil {
			return cliauth.Result{}, e
		}
	}
	settings, e := m.Startup.GetInitialSettings()
	if e != nil {
		return cliauth.Result{}, e
	}
	merged := protocol.NewJSONObject()
	if settings != nil {
		merged = settings.Clone()
	}
	producer := &cliauth.OAuthClient{Config: config, HTTP: client, NowMillis: m.Native.NowMillis, Environment: func() map[string]string { return m.Native.Environment }, Settings: func(context.Context) (protocol.JSONObject, error) { return merged.Clone(), nil }}
	browser := o.Browser
	if browser == nil {
		browser = m.authLoginBrowser
	}
	installed := func(ctx context.Context, fn func(*cooperative.Scope) error) error {
		receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Input, fn)
		if e == nil {
			e = receipt.Wait()
		}
		return e
	}
	producer.PreviousClaude = func(ctx context.Context) (protocol.JSONObject, protocol.JSONObject, error) {
		global, tokens := protocol.NewJSONObject(), protocol.NewJSONObject()
		e := installed(ctx, func(s *cooperative.Scope) error {
			value, e := m.Global.Get()
			if e != nil {
				return e
			}
			if value != nil {
				global = value.Clone()
			}
			raw, e := m.Secure.ReadSourceJSON(ctx)
			if e != nil {
				return e
			}
			var secure protocol.JSONObject
			_ = secure.UnmarshalJSON(raw)
			if old, ok := secure.Object("claudeAiOauth"); ok {
				tokens = old.Clone()
			}
			return nil
		})
		return global, tokens, e
	}
	producer.UpdateClaudeProfile = func(ctx context.Context, profile protocol.JSONObject) error {
		return installed(ctx, func(s *cooperative.Scope) error { return m.updateCLIRefreshProfile(profile) })
	}
	runtime := cliauth.LoginRuntime{Client: producer, Environment: m.Native.Environment, InitialSettings: merged, Write: func(s string) error { _, e := io.WriteString(m.Output, s); return e }, Browser: browser,
		InstallClaude: func(ctx context.Context, tokens protocol.JSONObject) error {
			return installed(ctx, func(s *cooperative.Scope) error { return m.installCLIClaudeTokens(ctx, s, producer, tokens, o.Version) })
		},
		SaveOpenAI: func(ctx context.Context, tokens protocol.JSONObject) error {
			return installed(ctx, func(s *cooperative.Scope) error { return m.saveCLIOpenAITokens(ctx, s, tokens) })
		},
		InstallOpenAI: func(ctx context.Context, tokens protocol.JSONObject) (string, error) {
			warning := ""
			e := installed(ctx, func(s *cooperative.Scope) error {
				var e error
				warning, e = m.installCLIOpenAITokens(ctx, s, tokens)
				return e
			})
			return warning, e
		},
		ValidateOrganization: func(ctx context.Context) (string, error) {
			message := ""
			e := installed(ctx, func(s *cooperative.Scope) error {
				var e error
				message, e = m.validateCLILoginOrganization(ctx, s, producer)
				return e
			})
			return message, e
		},
		MarkOnboarding: func(ctx context.Context) error {
			return installed(ctx, func(s *cooperative.Scope) error {
				return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					next := current.Clone()
					if string(next.Get("hasCompletedOnboarding")) != "true" {
						next.Set("hasCompletedOnboarding", []byte(`true`))
					}
					return &next, nil
				})
			})
		},
	}
	data, e := scope.AwaitBytes(cooperative.NetworkOpen, func(worker context.Context) ([]byte, error) {
		result, e := cliauth.Login(worker, o.Input, runtime)
		if e != nil {
			return nil, e
		}
		return json.Marshal(result)
	})
	if e != nil {
		return cliauth.Result{}, e
	}
	var result cliauth.Result
	if e = json.Unmarshal(data, &result); e != nil {
		return cliauth.Result{}, e
	}
	return result, nil
}

func (m *MainCommand) authLoginBrowser(ctx context.Context, address string) error {
	command := m.Native.Environment["BROWSER"]
	args := []string{address}
	if m.credentialPlatform == "windows" {
		if command == "" {
			command = "rundll32"
			args = []string{"url,OpenURL", address}
		} else {
			args = []string{`"` + address + `"`}
		}
	} else if command == "" {
		command = "xdg-open"
		if m.credentialPlatform == "darwin" {
			command = "open"
		}
	}
	c := exec.CommandContext(ctx, command, args...)
	c.Dir = m.Native.CWD
	for key, value := range m.Native.Environment {
		c.Env = append(c.Env, key+"="+value)
	}
	if e := c.Run(); e != nil {
		return fmt.Errorf("browser open failed: %s", e)
	}
	return nil
}
