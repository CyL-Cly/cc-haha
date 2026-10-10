//go:build !offline_native_fixture && !native_process_fixture

package main

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/application"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
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
	env, err = application.ResolveProcessEnvironment(context.Background(), storage.New(storage.Options{}), env, nil)
	if err != nil {
		return nil, err
	}
	return newNativeBoundaryForEnvironment(env, cwd, runtime.GOOS)
}
func newNativeBoundaryForEnvironment(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
	security, err := application.SmokeSecurity(env)
	if err != nil {
		return nil, err
	}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform})
	if err == nil {
		native.RunSecurity = security
	}
	return native, err
}
func writeNativeObservation(*clibootstrap.NativeBoundary) {}
