//go:build native_process_fixture && !offline_native_fixture

package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/testdata/gochildcoverage"
	"github.com/NanmiCoder/cc-haha/backend/testdata/keychainprefetchfixture"
)

// This build tag substitutes only external managed-policy primitives. All
// application, provider, query loop, tool, file, terminal and transport code
// remains the public implementation. Release builds exclude this file.
func newMainNativeBoundary() (*clibootstrap.NativeBoundary, error) {
	env := processEnvironment()
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
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("native fixture requires an existing physical private root")
	}
	rootInfo, err := os.Stat(physicalRoot)
	if err != nil || !rootInfo.IsDir() {
		return nil, errors.New("native fixture requires a physical private directory")
	}
	for _, key := range []string{"HOME", "TMPDIR"} {
		path := env[key]
		if !nativeFixtureWithinPhysicalRoot(physicalRoot, path) {
			return nil, errors.New("native fixture path escapes private root: " + key)
		}
	}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err == nil {
		// Validate the effective native filesystem root, including Source's
		// absent/empty/relative launch spellings, before any startup consumer.
		for _, path := range []string{native.CWD, native.ConfigHomeDirectory()} {
			if !nativeFixtureWithinPhysicalRoot(physicalRoot, path) {
				return nil, errors.New("native fixture effective directory escapes private root")
			}
		}
		if directory := env["GOCOVERDIR"]; directory != "" {
			if directory != os.Getenv("GOCOVERDIR") {
				return nil, errors.New("native fixture coverage must use its actual process environment")
			}
			if err := installNativeFixtureCoverageSignal(); err != nil {
				return nil, err
			}
			// The original main exit is serialized with the same attested
			// SIGHUP snapshot. It still performs the real normal os.Exit.
			exit = gochildcoverage.Exit
		}
		native.CCRCredentialDirectory = filepath.Join(root, "remote")
		// Disposable HOME alone does not isolate macOS Keychain. This test tag
		// substitutes the external secure primitive; release builds exclude it.
		native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
			return nil, errors.New("isolated native fixture Keychain unavailable")
		}
		// The test tag substitutes actual execFile admission as well as sync
		// reads. Each detached child is an owned empty process, never security.
		native.StartKeychainPrefetch = keychainprefetchfixture.Bind(native.Temp, native.CWD, native.Home, func(ctx context.Context, call securestorage.Command) ([]byte, error) {
			return native.RunSecurity(ctx, call)
		})
		// The same external init fetch must stay offline in fixture binaries.
		// Business once/transport gates and verified TLS remain production code.
		native.PreconnectHTTP = func(request *http.Request, actual *http.Client) (*http.Response, error) {
			allowed := func(request *http.Request) bool {
				return request != nil && request.Method == http.MethodHead && request.URL != nil && request.URL.Hostname() == "127.0.0.1" && request.URL.Port() != "" && request.URL.User == nil && (request.URL.Scheme == "http" || request.URL.Scheme == "https")
			}
			if actual == nil || !allowed(request) {
				return nil, errors.New("isolated native preconnect requires exact loopback HEAD")
			}
			client := *actual
			client.CheckRedirect = func(next *http.Request, previous []*http.Request) error {
				if !allowed(next) {
					return errors.New("isolated native preconnect redirect escapes loopback")
				}
				if actual.CheckRedirect != nil {
					return actual.CheckRedirect(next, previous)
				}
				return nil
			}
			return client.Do(request)
		}
	}
	return native, err
}

func writeNativeObservation(*clibootstrap.NativeBoundary) {}

func nativeFixtureCoverageDirectory(executable, directory string) (string, error) {
	return gochildcoverage.HangupDirectory(executable, directory)
}

func installNativeFixtureCoverageSignal() error {
	return gochildcoverage.InstallHangupSignal()
}

// Compare actual filesystem locations while preserving raw launch strings.
// New settings/tmp tails may not exist yet; resolve their closest existing
// ancestor, including symbolic links, then append only the missing tail.
func nativeFixturePhysicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("native fixture path must be absolute")
	}
	path = filepath.Clean(path)
	tail := []string{}
	for {
		physical, err := filepath.EvalSymlinks(path)
		if err == nil {
			return filepath.Join(append([]string{physical}, tail...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		// A dangling link is an existing object, not a missing path segment.
		// Do not discard it and accidentally treat its target as an inside tail.
		if info, statErr := os.Lstat(path); statErr == nil || !errors.Is(statErr, fs.ErrNotExist) {
			_ = info
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		tail = append([]string{filepath.Base(path)}, tail...)
		path = parent
	}
}

func nativeFixtureWithinPhysicalRoot(root, path string) bool {
	physical, err := nativeFixturePhysicalPath(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, physical)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !(len(relative) > 3 && relative[:3] == ".."+string(filepath.Separator))
}
