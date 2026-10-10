package clibootstrap

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"golang.org/x/text/unicode/norm"
	"os"
	"path/filepath"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainCommand) freshCLILoginOrganizationToken(ctx context.Context, scope *cooperative.Scope, client *cliauth.OAuthClient, retry int) (*openaiauth.Value, error) {
	_, tokens, _, _, err := m.cliAuthIdentity(ctx, scope, true)
	if err != nil || tokens == nil || !openaiauth.AuthTruthy(tokens.Get("refreshToken")) || !cliauth.OAuthTokenExpired(tokens.Get("expiresAt"), m.Native.NowMillis()) {
		return tokens, err
	}
	subscriber, err := openaiauth.ClaudeSubscriber(true, tokens)
	if err != nil || !subscriber {
		return tokens, err
	}
	m.Secure.Clear()
	_, tokens, _, _, err = m.cliAuthIdentity(ctx, scope, true)
	if err != nil || tokens == nil || !openaiauth.AuthTruthy(tokens.Get("refreshToken")) || !cliauth.OAuthTokenExpired(tokens.Get("expiresAt"), m.Native.NowMillis()) {
		return tokens, err
	}
	dir, present := m.Native.Environment["CLAUDE_CONFIG_DIR"]
	if !present {
		dir = filepath.Join(m.Native.Home, ".claude")
	}
	dir = norm.NFC.String(dir)
	if err = m.Native.check("oauth-refresh-directory", dir); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	release, err := m.Global.AcquireSourceDirectoryLease(dir)
	if err != nil {
		var busy *cliglobalconfig.SourceLeaseBusyError
		if errors.As(err, &busy) && retry < 5 {
			var random [8]byte
			if _, err = rand.Read(random[:]); err != nil {
				return nil, err
			}
			wait := time.Duration(1000+float64(binary.LittleEndian.Uint64(random[:])>>11)/float64(uint64(1)<<53)*1000) * time.Millisecond
			_, err = cliAuthPhysical(m, scope, func() (struct{}, error) {
				timer := time.NewTimer(wait)
				defer timer.Stop()
				select {
				case <-timer.C:
					return struct{}{}, nil
				case <-ctx.Done():
					return struct{}{}, context.Cause(ctx)
				}
			})
			if err != nil {
				return nil, err
			}
			return m.freshCLILoginOrganizationToken(ctx, scope, client, retry+1)
		}
		return tokens, nil
	}
	defer release()
	m.Secure.Clear()
	_, tokens, _, _, err = m.cliAuthIdentity(ctx, scope, true)
	if err != nil || tokens == nil || !openaiauth.AuthTruthy(tokens.Get("refreshToken")) || !cliauth.OAuthTokenExpired(tokens.Get("expiresAt"), m.Native.NowMillis()) {
		return tokens, err
	}
	refreshed, refreshError := cliAuthPhysical(m, scope, func() (protocol.JSONObject, error) {
		return client.RefreshClaude(ctx, currentTokenString(tokens, "refreshToken"), nil)
	})
	if refreshError == nil {
		if openaiauth.AuthTruthy(refreshed.Get("refreshToken")) && openaiauth.AuthTruthy(refreshed.Get("expiresAt")) {
			stored := protocol.NewJSONObject()
			for _, key := range []string{"accessToken", "refreshToken", "expiresAt", "scopes", "subscriptionType", "rateLimitTier"} {
				stored.Set(key, refreshed.Get(key))
			}
			_, _ = m.Secure.Change(ctx, func(current protocol.JSONObject) error { current.Set("claudeAiOauth", rawMain(stored)); return nil })
		}
	}
	m.Secure.Clear()
	_, tokens, _, _, err = m.cliAuthIdentity(ctx, scope, true)
	return tokens, err
}

func (m *MainCommand) updateCLIRefreshProfile(profile protocol.JSONObject) error {
	info := profile
	return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		account, ok := next.Object("oauthAccount")
		if !ok {
			return &next, nil
		}
		for _, key := range []string{"displayName", "billingType", "accountCreatedAt", "subscriptionCreatedAt", "hasExtraUsageEnabled"} {
			value := info.Get(key)
			if value == nil || string(value) == "null" {
				continue
			}
			if key == "hasExtraUsageEnabled" && string(value) != "true" && string(value) != "false" {
				continue
			}
			account.Set(key, value)
		}
		next.Set("oauthAccount", rawMain(account))
		return &next, nil
	})
}
