package cliauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const SetupTokenLifetime = float64(365 * 24 * 60 * 60)
const SetupTokenStartingMessage = "This will guide you through long-lived (1-year) auth token setup for your Claude account. Claude subscription required."

type SetupTokenEvent struct {
	State        string
	URL          string
	Message      string
	Token        string
	TokenRaw     json.RawMessage
	RetryWaiting bool
}
type SetupTokenRuntime struct {
	Client   *OAuthClient
	Settings protocol.JSONObject
	Random   io.Reader
	Browser  func(context.Context, string) error
	Event    func(SetupTokenEvent) error
	Manual   <-chan string
	Retry    <-chan struct{}
}

// SetupToken is ConsoleOAuthFlow's inference-only flow. Credential installation,
// logout, account mutation and org validation deliberately have no consumer here.
func SetupToken(ctx context.Context, runtime SetupTokenRuntime) error {
	if runtime.Client == nil || runtime.Browser == nil || runtime.Event == nil {
		return io.ErrUnexpectedEOF
	}
	random := runtime.Random
	if random == nil {
		random = rand.Reader
	}
	// OAuthService creates one verifier at mount and reuses it on retry.
	verifier, e := randomOAuthValue(random)
	if e != nil {
		return e
	}
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		e = setupTokenAttempt(ctx, runtime, verifier, random)
		if e == nil {
			return nil
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		message := e.Error()
		if hint := OAuthSSLErrorHint(e); hint != "" {
			message = hint
		}
		if e = runtime.Event(SetupTokenEvent{State: "error", Message: message}); e != nil {
			return e
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-runtime.Retry:
		}
		if e = runtime.Event(SetupTokenEvent{State: "about_to_retry"}); e != nil {
			return e
		}
		if e = setupTokenDelay(ctx, time.Second); e != nil {
			return e
		}
	}
}
func setupTokenDelay(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func setupTokenAttempt(ctx context.Context, r SetupTokenRuntime, verifier string, random io.Reader) error {
	state, e := randomOAuthValue(random)
	if e != nil {
		return e
	}
	listener := NewOAuthListener("/callback")
	listener.SetErrorRedirect(claudeSuccessURL(r.Client.Config))
	browserCtx, cancelBrowser := context.WithCancel(ctx)
	defer func() { cancelBrowser(); listener.Close() }()
	port, e := listener.Start(0)
	if e != nil {
		return e
	}
	challenge := sha256.Sum256([]byte(verifier))
	options := desktopoauth.CLIAuthURL{Challenge: base64.RawURLEncoding.EncodeToString(challenge[:]), State: state, Port: float64(port), Manual: true, ClaudeAI: true, InferenceOnly: true, Organization: r.Settings.String("forceLoginOrgUUID")}
	manual, e := desktopoauth.BuildCLIAuthURL(r.Client.Config, options)
	if e != nil {
		return e
	}
	options.Manual = false
	automatic, e := desktopoauth.BuildCLIAuthURL(r.Client.Config, options)
	if e != nil {
		return e
	}
	result := make(chan authorizationCode, 1)
	waiterDone := make(chan struct{})
	defer func() { cancelBrowser(); listener.Close(); <-waiterDone }()
	go func() {
		defer close(waiterDone)
		code, e := listener.Wait(browserCtx, state, func() error {
			if e := r.Event(SetupTokenEvent{State: "waiting_for_login", URL: manual}); e != nil {
				return e
			}
			// Original openBrowser failures don't reject the authorization promise.
			_ = r.Browser(browserCtx, automatic)
			return nil
		})
		result <- authorizationCode{code, e}
	}()
	manualFlow := false
	var code string
waiting:
	for {
		select {
		case value := <-result:
			if value.Err != nil {
				return value.Err
			}
			code = value.Code
			break waiting
		case value := <-r.Manual:
			parts := strings.Split(value, "#")
			if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
				if e = r.Event(SetupTokenEvent{State: "error", Message: "Invalid code. Please make sure the full code was copied", URL: manual, RetryWaiting: true}); e != nil {
					return e
				}
				// The original authorization promise remains active while the UI
				// displays an invalid paste. An automatic callback can still win.
				select {
				case value := <-result:
					if value.Err != nil {
						return value.Err
					}
					code = value.Code
					break waiting
				case <-ctx.Done():
					return context.Cause(ctx)
				case <-r.Retry:
				}
				if e = r.Event(SetupTokenEvent{State: "about_to_retry", RetryWaiting: true}); e != nil {
					return e
				}
				timer := time.NewTimer(time.Second)
				select {
				case value := <-result:
					timer.Stop()
					if value.Err != nil {
						return value.Err
					}
					code = value.Code
					break waiting
				case <-ctx.Done():
					timer.Stop()
					return context.Cause(ctx)
				case <-timer.C:
				}
				if e = r.Event(SetupTokenEvent{State: "waiting_for_login", URL: manual}); e != nil {
					return e
				}
				continue
			}
			// Source requires the UI's #state but manual resolver uses only the code;
			// exchange retains the initially generated state.
			code = parts[0]
			manualFlow = true
			cancelBrowser()
			listener.Close()
			<-result
			break waiting
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	tokens, e := r.Client.ExchangeClaude(ctx, code, state, verifier, port, manualFlow, SetupTokenLifetime)
	if e != nil {
		listener.Respond(302, http.Header{"Location": []string{claudeSuccessURL(r.Client.Config)}}, "")
		return e
	}
	redirect := claudeSuccessURL(r.Client.Config)
	var scopes []string
	decodeLogin(tokens.Get("scopes"), &scopes)
	if !hasScope(scopes, "user:inference") {
		redirect = consoleSuccessURL(r.Client.Config)
	}
	listener.Respond(302, http.Header{"Location": []string{redirect}}, "")
	cancelBrowser()
	listener.Close()
	if e = r.Event(SetupTokenEvent{State: "success", Token: setupTokenVisibleText(tokens.Get("accessToken")), TokenRaw: tokens.Get("accessToken")}); e != nil {
		return e
	}
	return setupTokenDelay(ctx, 500*time.Millisecond)
}

func SetupTokenSuccessText(token string) string {
	if token == "" {
		return ""
	}
	return "✓ Long-lived authentication token created successfully!\n\nYour OAuth token (valid for 1 year):\n" + token + "\n\nStore this token securely. You won't be able to see it again.\n\nUse this token by setting: export CLAUDE_CODE_OAUTH_TOKEN=<token>\n"
}

// The OAuth token contract is a string. Missing, null and false values must
// not manufacture visible token text. Keep the original scalar presentation
// coercion for a non-string scalar response.
func setupTokenVisibleText(raw json.RawMessage) string {
	if !openaiauth.AuthTruthy(raw) {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return OAuthFieldString(raw)
	case []any:
		var out strings.Builder
		for _, x := range v {
			b, _ := json.Marshal(x)
			out.WriteString(setupTokenVisibleText(b))
		}
		return out.String()
	}
	return ""
}
