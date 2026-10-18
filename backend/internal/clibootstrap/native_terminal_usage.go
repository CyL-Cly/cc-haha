package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type TerminalUsageHTTPError struct {
	Status int
	Body   json.RawMessage
}

func (e *TerminalUsageHTTPError) Error() string {
	return fmt.Sprintf("Request failed with status code %d", e.Status)
}

func terminalUsageHasProfile(tokens *openaiauth.Value) bool {
	if tokens == nil {
		return false
	}
	var scopes []json.RawMessage
	if json.Unmarshal(tokens.Get("scopes"), &scopes) == nil {
		for _, scope := range scopes {
			if string(scope) == `"user:profile"` {
				return true
			}
		}
		return false
	}
	var scopesText string
	if json.Unmarshal(tokens.Get("scopes"), &scopesText) == nil {
		return bytes.Contains([]byte(scopesText), []byte("user:profile"))
	}
	return false
}
func terminalUsageExpired(raw json.RawMessage, now float64) bool {
	if string(raw) == "null" || len(raw) == 0 {
		return false
	}
	var expiry float64
	if json.Unmarshal(raw, &expiry) != nil {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return false
		}
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return false
		}
		expiry = parsed
	}
	return now+5*60*1000 >= expiry
}

// TerminalUsage consumes the existing cached identity and original endpoint.
// Snapshot and completion belong to this same Owner. The mounted component's
// HTTP promise does not retain a REPL/ASK stack while awaiting the transport.
func (m *MainPrint) TerminalUsage(ctx context.Context) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	var completion *ownerasync.OwnerFuture[struct{}]
	var physicalDone chan struct{}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.Host == nil {
			return cooperative.ErrClosed
		}
		identity, tokens, _, enabled, err := m.terminalStatusAuth(ctx, scope)
		if err != nil {
			return err
		}
		subscriber, err := openaiauth.ClaudeSubscriber(enabled, tokens)
		if err != nil {
			return err
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "utilization", Value: []byte(`{}`)}, protocol.JSONField{Name: "subscriptionType", Value: []byte(`null`)}, protocol.JSONField{Name: "extraUsageCommandEnabled", Value: []byte(`false`)}, protocol.JSONField{Name: "now", Value: quoteMain(m.options.Native.Now().Format(time.RFC3339Nano))}, protocol.JSONField{Name: "timezone", Value: quoteMain(m.nativeOpenAIEnvironment()["TZ"])})
		if subscriber && tokens != nil {
			if value := tokens.Get("subscriptionType"); len(value) > 0 {
				result.Set("subscriptionType", value)
			}
		}
		account := mainObj(identity.OAuthAccount)
		billing := account.String("billingType")
		allowedBilling := billing == "stripe_subscription" || billing == "stripe_subscription_contracted" || billing == "apple_subscription" || billing == "google_play_subscription"
		result.Set("extraUsageCommandEnabled", rawMain(subscriber && allowedBilling && m.options.Interactive && !nativeEnvTruthy(m.nativeOpenAIEnvironment()["DISABLE_EXTRA_USAGE_COMMAND"])))
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		if global != nil {
			if cache, ok := global.Object("overageCreditGrantCache"); ok {
				if entry, ok := cache.Object(account.String("organizationUuid")); ok {
					var stamp float64
					_ = json.Unmarshal(entry.Get("timestamp"), &stamp)
					if m.options.Native.NowMillis()-stamp <= 60*60*1000 {
						result.Set("overageCreditGrant", entry.Get("info"))
					}
				}
			}
		}
		if !subscriber || !terminalUsageHasProfile(tokens) {
			return nil
		}
		if terminalUsageExpired(tokens.Get("expiresAt"), m.options.Native.NowMillis()) {
			result.Set("utilization", []byte(`null`))
			return nil
		}
		access := tokens.Object.String("accessToken")
		if access == "" {
			return fmt.Errorf("Auth error: No OAuth token available")
		}
		env := m.nativeOpenAIEnvironment()
		config, err := desktopoauth.ResolveConfig(env)
		if err != nil {
			return err
		}
		tls, err := tlsconfig.NewState(m.options.Native.readFile)
		if err != nil {
			return err
		}
		client, err := webfetch.NewHTTPClient(env, tls)
		if err != nil {
			return err
		}
		physicalDone = make(chan struct{})
		future, err := nativeSkillWork(m, scope, func() (json.RawMessage, error) {
			defer close(physicalDone)
			requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, config.BaseAPIURL+"/api/oauth/usage", nil)
			if err != nil {
				return nil, err
			}
			request.Header.Set("Accept", "application/json, text/plain, */*")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("User-Agent", "claude-code/"+provider.SourceCompatibilityVersion)
			request.Header.Set("Authorization", "Bearer "+access)
			request.Header.Set("anthropic-beta", "oauth-2025-04-20")
			request.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
			response, err := client.Do(request)
			if err != nil {
				if ctx.Err() == nil && requestCtx.Err() != nil {
					return nil, fmt.Errorf("timeout of 5000ms exceeded")
				}
				return nil, err
			}
			defer response.Body.Close()
			body, err := webfetch.ReadAxiosResponseBody(response)
			if err != nil {
				return nil, err
			}
			body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
			body = protocol.DecodeJavaScriptUTF8(body)
			if !json.Valid(body) {
				body = quoteMain(string(body))
			}
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return nil, &TerminalUsageHTTPError{response.StatusCode, body}
			}
			return body, nil
		})
		if err != nil {
			return err
		}
		completion, err = future.Then(scope, func(_ *cooperative.Scope, body json.RawMessage, failure error) error {
			if failure != nil {
				return failure
			}
			result.Set("utilization", append(json.RawMessage(nil), body...))
			return nil
		})
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err == nil && completion != nil {
		_, err = completion.Wait(ctx)
		if ctx.Err() != nil {
			// Cancellation stops the original HTTP context, then joins the same
			// owned completion. A dismissed component cannot leave native work.
			_, _ = completion.Wait(context.Background())
			<-physicalDone
		}
	}
	return result, err
}
