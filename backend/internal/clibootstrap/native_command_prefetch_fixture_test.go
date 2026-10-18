package clibootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/testdata/keychainprefetchfixture"
)

// Existing fixture callbacks supply fake storage contents, not subprocess
// admission. Materialize those fixture bytes through an actual owned process.
// Held-process tests bind their own Start primitive. No machine security
// executable is ever selected, including when a fixture forgot its callback.
func bindCommandPrefetchFixture(t *testing.T, n *NativeBoundary) {
	t.Helper()
	n.StartKeychainPrefetch = keychainprefetchfixture.Bind(n.Temp, n.CWD, n.Home, func(ctx context.Context, call securestorage.Command) ([]byte, error) {
		if n.RunSecurity == nil {
			return nil, errors.New("unbound private prefetch fixture read")
		}
		return n.RunSecurity(ctx, call)
	})
}
