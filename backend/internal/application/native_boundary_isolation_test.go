package application

import (
	"context"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
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
	return boundary, nil
}
