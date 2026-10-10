package cliauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type LoginInput struct {
	Email                          string
	SSO, Console, ClaudeAI, OpenAI bool
}
type LoginRuntime struct {
	Client               *OAuthClient
	Environment          map[string]string
	InitialSettings      protocol.JSONObject
	Random               io.Reader
	Write                func(string) error
	Browser              func(context.Context, string) error
	InstallClaude        func(context.Context, protocol.JSONObject) error
	SaveOpenAI           func(context.Context, protocol.JSONObject) error
	InstallOpenAI        func(context.Context, protocol.JSONObject) (string, error)
	ValidateOrganization func(context.Context) (string, error)
	MarkOnboarding       func(context.Context) error
}

func Login(ctx context.Context, input LoginInput, runtime LoginRuntime) (Result, error) {
	if input.OpenAI && (input.Email != "" || input.SSO || input.Console || input.ClaudeAI) {
		return Result{ExitCode: 1, Stderr: "Error: --openai cannot be combined with --email, --sso, --console, or --claudeai.\n"}, nil
	}
	if input.Console && input.ClaudeAI {
		return Result{ExitCode: 1, Stderr: "Error: --console and --claudeai cannot be used together.\n"}, nil
	}
	if runtime.Client == nil || runtime.Write == nil || runtime.Browser == nil || runtime.InstallClaude == nil || runtime.ValidateOrganization == nil || runtime.MarkOnboarding == nil {
		return Result{}, io.ErrUnexpectedEOF
	}
	if input.OpenAI {
		return loginOpenAI(ctx, input, runtime)
	}
	if refresh := runtime.Environment["CLAUDE_CODE_OAUTH_REFRESH_TOKEN"]; refresh != "" {
		rawScopes := runtime.Environment["CLAUDE_CODE_OAUTH_SCOPES"]
		if rawScopes == "" {
			return Result{ExitCode: 1, Stderr: "CLAUDE_CODE_OAUTH_SCOPES is required when using CLAUDE_CODE_OAUTH_REFRESH_TOKEN.\nSet it to the space-separated scopes the refresh token was issued with\n(e.g. \"user:inference\" or \"user:profile user:inference user:sessions:claude_code user:mcp_servers\").\n"}, nil
		}
		scopes := strings.FieldsFunc(rawScopes, protocol.IsJavaScriptWhitespace)
		tokens, e := runtime.Client.RefreshClaude(ctx, refresh, scopes)
		if e == nil {
			e = runtime.InstallClaude(ctx, tokens)
		}
		if e != nil {
			return Result{ExitCode: 1, Stderr: claudeLoginError(e)}, nil
		}
		message, e := runtime.ValidateOrganization(ctx)
		if e != nil {
			return Result{ExitCode: 1, Stderr: claudeLoginError(e)}, nil
		}
		if message != "" {
			return Result{ExitCode: 1, Stderr: message + "\n"}, nil
		}
		if e = runtime.MarkOnboarding(ctx); e != nil {
			return Result{ExitCode: 1, Stderr: claudeLoginError(e)}, nil
		}
		return Result{Stdout: "Login successful.\n"}, nil
	}
	random := runtime.Random
	if random == nil {
		random = rand.Reader
	}
	verifier, e := randomOAuthValue(random)
	if e != nil {
		return Result{}, e
	}
	state, e := randomOAuthValue(random)
	if e != nil {
		return Result{}, e
	}
	challenge := sha256.Sum256([]byte(verifier))
	listener := NewOAuthListener("/callback")
	listener.SetErrorRedirect(claudeSuccessURL(runtime.Client.Config))
	browserContext, cancelBrowser := context.WithCancel(ctx)
	defer func() { cancelBrowser(); listener.Close() }()
	port, e := listener.Start(0)
	if e != nil {
		return Result{ExitCode: 1, Stderr: claudeLoginError(e)}, nil
	}
	claudeAI := !input.Console
	if method := runtime.InitialSettings.String("forceLoginMethod"); method != "" {
		claudeAI = method == "claudeai"
	}
	o := desktopoauth.CLIAuthURL{Challenge: base64.RawURLEncoding.EncodeToString(challenge[:]), State: state, Port: float64(port), ClaudeAI: claudeAI, Organization: runtime.InitialSettings.String("forceLoginOrgUUID"), Email: input.Email}
	if input.SSO {
		o.LoginMethod = "sso"
	}
	o.Manual = true
	manual, e := desktopoauth.BuildCLIAuthURL(runtime.Client.Config, o)
	if e != nil {
		return Result{}, e
	}
	o.Manual = false
	automatic, e := desktopoauth.BuildCLIAuthURL(runtime.Client.Config, o)
	if e != nil {
		return Result{}, e
	}
	code, e := listener.Wait(ctx, state, func() error {
		if e := runtime.Write("Opening browser to sign in…\nIf the browser didn't open, visit: " + manual + "\n"); e != nil {
			return e
		}
		_ = runtime.Browser(browserContext, automatic)
		return nil
	})
	if e != nil {
		return Result{ExitCode: 1, Stderr: claudeLoginError(e)}, nil
	}
	tokens, e := runtime.Client.ExchangeClaude(ctx, code, state, verifier, port, !listener.HasPending())
	if e != nil {
		listener.Respond(302, http.Header{"Location": []string{claudeSuccessURL(runtime.Client.Config)}}, "")
		return Result{ExitCode: 1, Stderr: claudeLoginError(e)}, nil
	}
	redirect := claudeSuccessURL(runtime.Client.Config)
	var scopes []string
	decodeLogin(tokens.Get("scopes"), &scopes)
	if !hasScope(scopes, "user:inference") {
		redirect = consoleSuccessURL(runtime.Client.Config)
	}
	listener.Respond(302, http.Header{"Location": []string{redirect}}, "")
	cancelBrowser()
	listener.Close()
	if e = runtime.InstallClaude(ctx, tokens); e != nil {
		return Result{ExitCode: 1, Stderr: claudeLoginError(e)}, nil
	}
	message, e := runtime.ValidateOrganization(ctx)
	if e != nil {
		return Result{ExitCode: 1, Stderr: claudeLoginError(e)}, nil
	}
	if message != "" {
		return Result{ExitCode: 1, Stderr: message + "\n"}, nil
	}
	return Result{Stdout: "Login successful.\n"}, nil
}
func randomOAuthValue(reader io.Reader) (string, error) {
	data := make([]byte, 32)
	if _, e := io.ReadFull(reader, data); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
func hasScope(scopes []string, value string) bool {
	for _, scope := range scopes {
		if scope == value {
			return true
		}
	}
	return false
}
func claudeSuccessURL(c desktopoauth.Config) string {
	return strings.TrimSuffix(c.ManualRedirectURL, "/callback") + "/success?app=claude-code"
}
func consoleSuccessURL(c desktopoauth.Config) string {
	base := strings.TrimSuffix(c.ManualRedirectURL, "/oauth/code/callback")
	if c.FileSuffix == "-custom-oauth" {
		return claudeSuccessURL(c)
	}
	return base + "/buy_credits?returnUrl=/oauth/code/success%3Fapp%3Dclaude-code"
}

func decodeLogin(raw json.RawMessage, target any) { _ = json.Unmarshal(raw, target) }
