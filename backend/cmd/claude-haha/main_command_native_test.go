package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/testdata/keychainprefetchfixture"
)

// Replay the public action inside the test process as well as in the existing
// compiled CLI. Only the absent OS policy and Keychain stores are substituted;
// parsing, startup, settings, transport, actions and their lifetimes stay real.
func nativeCommandConsumerBoundary(t *testing.T, root, home string, environment []string) *clibootstrap.NativeBoundary {
	t.Helper()
	env := map[string]string{}
	for _, item := range environment {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			env[key] = value
		}
	}
	for _, key := range []string{"HOME", "USERPROFILE", "CLAUDE_CONFIG_DIR", "TMPDIR"} {
		relative, err := filepath.Rel(root, env[key])
		if err != nil || !filepath.IsAbs(env[key]) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			t.Fatalf("private command path escapes root: %s", key)
		}
	}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{
		Environment: env, CWD: home, Platform: runtime.GOOS, ManagedRoot: filepath.Join(root, "managed"),
		ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	native.CCRCredentialDirectory = filepath.Join(root, "remote")
	native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	native.StartKeychainPrefetch = keychainprefetchfixture.Bind(native.Temp, native.CWD, native.Home, func(ctx context.Context, call securestorage.Command) ([]byte, error) {
		return native.RunSecurity(ctx, call)
	})
	native.PreconnectHTTP = nativeCommandFixturePreconnect
	return native
}
