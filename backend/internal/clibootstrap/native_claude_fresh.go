package clibootstrap

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
)

// checkClaudeTokensBeforeClient ports auth.ts's local disk staleness check.
// The client's model AbortSignal is not an input to this shared auth producer.
// An expired, refreshable account requires the still-unported refresh consumer;
// it cannot silently borrow the future-expiry/no-refresh-token profile.
func (b *NativeOpenAIAuth) checkClaudeTokensBeforeClient(ctx context.Context, scope *cooperative.Scope) (failure error) {
	ctx, err := b.owned(ctx, scope)
	if err != nil {
		return err
	}
	defer func() {
		if failure == nil {
			// getAnthropicClient separately awaits this async check's return.
			failure = scope.AwaitFulfilled()
		}
	}()
	path := filepath.Join(b.main.options.Native.ConfigHomeDirectory(), ".credentials.json")
	future, err := nativeSkillWork(b.main, scope, func() (float64, error) {
		info, err := b.main.options.Native.stat(path)
		if err != nil {
			return 0, err
		}
		return float64(info.ModTime().UnixNano()) / 1e6, nil
	})
	if err != nil {
		return err
	}
	mtime, statError := future.Await(scope)
	if statError != nil {
		// Source catches every stat failure and only clears getClaudeAIOAuthTokens,
		// retaining the secure-storage cache's existing expiry policy.
		if statError == cooperative.ErrClosed || statError == cooperative.ErrRestricted {
			return statError
		}
		b.claudeTokens, b.claudeTokensLoaded = nil, false
	} else if mtime != b.lastCredentialsMtimeMillis {
		b.lastCredentialsMtimeMillis = mtime
		if err := b.ClearClaudeTokenCache(scope); err != nil {
			return err
		}
	}
	// invalidateOAuthCacheIfDiskChanged's async return is awaited by the
	// original checkAndRefreshOAuthTokenIfNeededImpl caller.
	if err := scope.AwaitFulfilled(); err != nil {
		return err
	}
	env := b.main.nativeOpenAIEnvironment()
	tokens, err := b.claudeTokensFor(ctx, env, envTruthyNative(env["CLAUDE_CODE_SIMPLE"]))
	if err != nil || tokens == nil || !openaiauth.AuthTruthy(tokens.Get("refreshToken")) {
		return err
	}
	expiry := tokens.Get("expiresAt")
	if string(expiry) == "null" {
		return nil
	}
	var expiresAt float64
	if err := json.Unmarshal(expiry, &expiresAt); err != nil {
		return mainCap("Claude OAuth expiry original number")
	}
	if b.main.options.Native.NowMillis()+5*60*1000 < expiresAt {
		return nil
	}
	refreshable, err := openaiauth.ClaudeSubscriber(true, tokens)
	if err != nil || !refreshable {
		return err
	}
	return mainCap("Claude OAuth expired refreshable token producer")
}
