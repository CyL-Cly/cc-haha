package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
)

// Source's memoized getApiKeyFromConfigOrMacOSKeychain is shared by source
// selection and conflict notices. Failure/cancellation ordering stays exact.
func (b *NativeOpenAIAuth) ensureLegacyAPIKeyCached(ctx context.Context) error {
	if b.legacyAPIKeyLoaded {
		return nil
	}
	key, err := b.main.secure.ReadLegacyManagedAPIKey(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		if reportError := b.main.nativeLogError(ctx, err); reportError != nil {
			return reportError
		}
	}
	if key == "" {
		global, err := b.main.Global.Get()
		if err != nil {
			return err
		}
		if global != nil {
			key = global.String("primaryApiKey")
			b.legacyAPIKeyPresent = openaiauth.AuthTruthy(global.Get("primaryApiKey"))
		}
	}
	b.legacyAPIKeyPresent = b.legacyAPIKeyPresent || key != ""
	b.legacyAPIKey, b.legacyAPIKeyLoaded = key, true
	return nil
}
