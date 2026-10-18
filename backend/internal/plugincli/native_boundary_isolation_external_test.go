package plugincli_test

import (
	"context"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/testdata/keychainprefetchfixture"
)

// A disposable HOME does not isolate the macOS Keychain. Every reconstructed
// boundary used by these native consumers explicitly substitutes that external
// primitive; all settings, permissions, Owner and business code stay actual.
func newIsolatedProcessNativeBoundary(options clibootstrap.NativeProcessOptions) (*clibootstrap.NativeBoundary, error) {
	boundary, err := clibootstrap.NewProcessNativeBoundary(options)
	if err != nil {
		return nil, err
	}
	boundary.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	boundary.StartKeychainPrefetch = keychainprefetchfixture.Bind(boundary.Temp, boundary.CWD, boundary.Home, func(ctx context.Context, call securestorage.Command) ([]byte, error) {
		return boundary.RunSecurity(ctx, call)
	})
	return boundary, nil
}
