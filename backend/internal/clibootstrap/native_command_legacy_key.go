package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

type nativeCommandLegacyAPIKeyMemo struct {
	store   *securestorage.Store
	key     string
	present bool
}

// auth.ts getApiKeyFromConfigOrMacOSKeychain memoizes both a key and absence.
// This is the command process's getter, not a cache in generic SecureStorage.
// OAuth selection remains independent, including forced-token early return.
func (m *MainCommand) nativeCommandLegacyAPIKey(ctx context.Context, scope *cooperative.Scope) (string, bool, error) {
	if m == nil || scope == nil || !scope.BelongsTo(m.Owner) || m.Secure == nil || m.Global == nil {
		return "", false, cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return "", false, err
	}
	if err := context.Cause(ctx); err != nil {
		return "", false, err
	}
	if memo := m.commandLegacyAPIKey; memo != nil {
		if memo.store != m.Secure {
			return "", false, cooperative.ErrRestricted
		}
		return memo.key, memo.present, nil
	}
	key, completed := "", false
	if runtime := m.nativeKeychainPrefetch; runtime != nil {
		if err := runtime.check(scope); err != nil {
			return "", false, err
		}
		key, completed = runtime.ticket.LegacyResult()
	}
	var err error
	if !completed {
		key, err = m.Secure.ReadLegacyManagedAPIKey(ctx)
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", false, context.Cause(ctx)
		}
		// Original catches a failed sync primitive, logs it, and still reads
		// primaryApiKey. A canceled read must not become a cached absence.
		if m.commandErrors != nil {
			m.commandErrors.Log(err)
		}
	}
	present := key != ""
	if !present {
		global, failure := m.Global.Get()
		if failure != nil {
			return "", false, failure
		}
		if global != nil {
			key, present = global.String("primaryApiKey"), openaiauth.AuthTruthy(global.Get("primaryApiKey"))
		}
	}
	m.commandLegacyAPIKey = &nativeCommandLegacyAPIKeyMemo{store: m.Secure, key: key, present: present}
	return key, present, nil
}
