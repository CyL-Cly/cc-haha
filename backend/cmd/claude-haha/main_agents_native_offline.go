//go:build offline_native_fixture

package main

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"os"
	"strings"
)

func newMainAgentsNativeBoundary() (*clibootstrap.NativeBoundary, error) {
	env := map[string]string{}
	for _, s := range os.Environ() {
		k, v, ok := strings.Cut(s, "=")
		if ok {
			env[k] = v
		}
	}
	cwd, e := os.Getwd()
	if e != nil {
		return nil, e
	}
	return clibootstrap.NewOfflineAgentsNativeBoundary(env, cwd)
}
