//go:build native_process_fixture && !offline_native_fixture

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/application"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// This build tag substitutes only external managed-policy primitives. All
// application, provider, query loop, tool, file, terminal and transport code
// remains the public implementation. Release builds exclude this file.
func newMainNativeBoundary() (*clibootstrap.NativeBoundary, error) {
	env := processEnvironment()
	var err error
	env, err = application.ResolveProcessEnvironment(context.Background(), storage.New(storage.Options{}), env, nil)
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return newNativeBoundaryForEnvironment(env, cwd, runtime.GOOS)
}

func newNativeBoundaryForEnvironment(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
	root := env["CC_HAHA_NATIVE_FIXTURE_ROOT"]
	if !filepath.IsAbs(root) {
		return nil, errors.New("native fixture requires an explicit private root")
	}
	for _, key := range []string{"HOME", "CLAUDE_CONFIG_DIR", "TMPDIR"} {
		path := env[key]
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || filepath.IsAbs(rel) || len(rel) > 3 && rel[:3] == "../" {
			return nil, errors.New("native fixture path escapes private root: " + key)
		}
	}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err == nil {
		native.CCRCredentialDirectory = filepath.Join(root, "remote")
		// Disposable HOME alone does not isolate macOS Keychain. This test tag
		// substitutes the external secure primitive; release builds exclude it.
		native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
			return nil, errors.New("isolated native fixture Keychain unavailable")
		}
	}
	return native, err
}

func writeNativeObservation(*clibootstrap.NativeBoundary) {}
