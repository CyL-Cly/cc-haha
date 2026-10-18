//go:build !offline_native_fixture && !native_process_fixture

package main

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/application"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"os"
	"runtime"
	"strings"
)

func newMainNativeBoundary() (*clibootstrap.NativeBoundary, error) {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	// Saved provider environment is applied by the ordinary CLI's existing
	// managed-settings startup stage. This OS snapshot must preserve raw launch
	// keys and must not read or repair provider state before that stage.
	return newNativeBoundaryForEnvironment(env, cwd, runtime.GOOS)
}
func newNativeBoundaryForEnvironment(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
	security, err := application.SmokeSecurity(env)
	if err != nil {
		return nil, err
	}
	prefetch, err := application.SmokeSecurityPrefetch(env)
	if err != nil {
		return nil, err
	}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform})
	if err == nil {
		native.RunSecurity = security
		native.StartKeychainPrefetch = prefetch
	}
	return native, err
}
func writeNativeObservation(*clibootstrap.NativeBoundary) {}
