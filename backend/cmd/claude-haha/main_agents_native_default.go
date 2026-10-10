//go:build !offline_native_fixture

package main

import "github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"

func newMainAgentsNativeBoundary() (*clibootstrap.NativeBoundary, error) {
	return newMainNativeBoundary()
}
