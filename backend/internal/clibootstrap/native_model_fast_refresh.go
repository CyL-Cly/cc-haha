package clibootstrap

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"golang.org/x/text/unicode/norm"
	"os"
	"path/filepath"
	"time"
)

// Each async phase returns immutable bytes to an original Owner reaction.
// No background Read callback is granted an ASK/provider continuation.
func (m *MainPrint) startNativeModelFastTokenRead(scope *cooperative.Scope, ctx context.Context, after func(*cooperative.Scope, *openaiauth.Value, error) error) error {
	if err := m.openAIAuth.ClearClaudeTokenCache(scope); err != nil {
		return after(scope, nil, err)
	}
	env := m.nativeOpenAIEnvironment()
	if env["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
		owned, err := m.openAIAuth.owned(ctx, scope)
		if err != nil {
			return after(scope, nil, err)
		}
		tokens, err := m.openAIAuth.claudeTokensFor(owned, env, pickerTrue(env["CLAUDE_CODE_SIMPLE"]))
		return after(scope, tokens, err)
	}
	return m.nativeModelFastWork(scope, ctx, func() ([]byte, error) { raw, err := m.secure.ReadSourceJSONAsync(ctx); return raw, err }, func(next *cooperative.Scope, raw []byte, failure error) error {
		if ctx.Err() != nil {
			return after(next, nil, context.Cause(ctx))
		}
		value := openaiauth.ClaudeTokensFromSecure(raw)
		m.openAIAuth.claudeTokens, m.openAIAuth.claudeTokensLoaded = value, true
		return after(next, value, failure)
	})
}
func (m *MainPrint) startNativeModelFast401(scope *cooperative.Scope, ctx context.Context, failed string, proceed func(*cooperative.Scope) error) error {
	return m.startNativeModelFastTokenRead(scope, ctx, func(next *cooperative.Scope, tokens *openaiauth.Value, failure error) error {
		if failure != nil || tokens == nil || !openaiauth.AuthTruthy(tokens.Get("refreshToken")) || tokens.Object.String("accessToken") != failed {
			return proceed(next)
		}
		subscriber, err := openaiauth.ClaudeSubscriber(true, tokens)
		if err != nil || !subscriber {
			return proceed(next)
		}
		return m.startNativeModelFastRefresh(next, ctx, 0, proceed)
	})
}
func (m *MainPrint) startNativeModelFastRefresh(scope *cooperative.Scope, ctx context.Context, retry int, proceed func(*cooperative.Scope) error) error {
	return m.startNativeModelFastTokenRead(scope, ctx, func(next *cooperative.Scope, tokens *openaiauth.Value, failure error) error {
		// Source force skips the first cached expiry check, but preserves this
		// fresh-store expiry check and the check after directory-lease acquisition.
		if failure != nil || tokens == nil || !openaiauth.AuthTruthy(tokens.Get("refreshToken")) || !cliauth.OAuthTokenExpired(tokens.Get("expiresAt"), m.options.Native.NowMillis()) {
			return proceed(next)
		}
		dir, present := m.options.Native.Environment["CLAUDE_CONFIG_DIR"]
		if !present {
			dir = filepath.Join(m.options.Native.Home, ".claude")
		}
		dir = norm.NFC.String(dir)
		if err := m.options.Native.check("oauth-refresh-directory", dir); err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		release, err := m.Global.AcquireSourceDirectoryLease(dir)
		if err != nil {
			var busy *cliglobalconfig.SourceLeaseBusyError
			if errors.As(err, &busy) && retry < 5 {
				var random [8]byte
				if _, err = rand.Read(random[:]); err != nil {
					return err
				}
				wait := time.Duration(1000+float64(binary.LittleEndian.Uint64(random[:])>>11)/float64(uint64(1)<<53)*1000) * time.Millisecond
				return m.nativeModelFastWork(next, ctx, func() ([]byte, error) {
					timer := time.NewTimer(wait)
					defer timer.Stop()
					select {
					case <-timer.C:
						return nil, nil
					case <-ctx.Done():
						return nil, context.Cause(ctx)
					}
				}, func(after *cooperative.Scope, _ []byte, failure error) error {
					if failure != nil {
						return proceed(after)
					}
					return m.startNativeModelFastRefresh(after, ctx, retry+1, proceed)
				})
			}
			return proceed(next)
		}
		return m.startNativeModelFastTokenRead(next, ctx, func(locked *cooperative.Scope, current *openaiauth.Value, failure error) error {
			if failure != nil || current == nil || !openaiauth.AuthTruthy(current.Get("refreshToken")) || !cliauth.OAuthTokenExpired(current.Get("expiresAt"), m.options.Native.NowMillis()) {
				release()
				return proceed(locked)
			}
			return m.startNativeModelFastRefreshHTTP(locked, ctx, current, func(after *cooperative.Scope) error { release(); return proceed(after) })
		})
	})
}
func (m *MainPrint) startNativeModelFastRefreshHTTP(scope *cooperative.Scope, ctx context.Context, tokens *openaiauth.Value, proceed func(*cooperative.Scope) error) error {
	env := m.nativeOpenAIEnvironment()
	config, err := m.nativeModelFastConfig(env)
	if err != nil {
		return proceed(scope)
	}
	http, err := webfetch.NewHTTPClient(env, m.tls)
	if err != nil {
		return proceed(scope)
	}
	global, err := m.Global.Get()
	if err != nil {
		return err
	}
	previous := tokens.Object.Clone()
	refresh := previous.String("refreshToken")
	return m.nativeModelFastWork(scope, ctx, func() ([]byte, error) {
		var profile *protocol.JSONObject
		client := &cliauth.OAuthClient{Config: config, HTTP: http, NowMillis: m.options.Native.NowMillis, PreviousClaude: func(context.Context) (protocol.JSONObject, protocol.JSONObject, error) {
			return global.Clone(), previous.Clone(), nil
		}, UpdateClaudeProfile: func(_ context.Context, value protocol.JSONObject) error {
			copy := value.Clone()
			profile = &copy
			return nil
		}}
		value, err := client.RefreshClaude(ctx, refresh, nil)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Tokens  protocol.JSONObject
			Profile *protocol.JSONObject
		}{value, profile})
	}, func(next *cooperative.Scope, raw []byte, failure error) error {
		if failure == nil && ctx.Err() == nil {
			var value struct {
				Tokens  protocol.JSONObject
				Profile *protocol.JSONObject
			}
			if err = json.Unmarshal(raw, &value); err != nil {
				return err
			}
			if value.Profile != nil {
				if err = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					next := current.Clone()
					account, exists := next.Object("oauthAccount")
					if exists {
						for _, key := range []string{"displayName", "billingType", "accountCreatedAt", "subscriptionCreatedAt", "hasExtraUsageEnabled"} {
							field := value.Profile.Get(key)
							if field != nil && string(field) != "null" {
								account.Set(key, field)
							}
						}
						next.Set("oauthAccount", rawMain(account))
					}
					return &next, nil
				}); err != nil {
					return err
				}
			}
			refreshed := value.Tokens
			var scopes []string
			_ = json.Unmarshal(refreshed.Get("scopes"), &scopes)
			inference := false
			for _, s := range scopes {
				inference = inference || s == "user:inference"
			}
			if inference && openaiauth.AuthTruthy(refreshed.Get("refreshToken")) && openaiauth.AuthTruthy(refreshed.Get("expiresAt")) {
				_, _ = m.secure.Change(ctx, func(current protocol.JSONObject) error {
					existing, _ := current.Object("claudeAiOauth")
					stored := protocol.NewJSONObject()
					for _, key := range []string{"accessToken", "refreshToken", "expiresAt", "scopes", "subscriptionType", "rateLimitTier"} {
						field := refreshed.Get(key)
						if (key == "subscriptionType" || key == "rateLimitTier") && (field == nil || string(field) == "null") {
							field = existing.Get(key)
							if field == nil {
								field = []byte("null")
							}
						}
						stored.Set(key, field)
					}
					current.Set("claudeAiOauth", rawMain(stored))
					return nil
				})
			}
		}
		if err = m.openAIAuth.ClearClaudeTokenCache(next); err != nil {
			return err
		}
		return proceed(next)
	})
}
