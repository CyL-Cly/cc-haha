//go:build !native_process_fixture

package main

import "github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"

func cliAuthLoginOptions(*clibootstrap.NativeBoundary) clibootstrap.AuthLoginOptions {
	return clibootstrap.AuthLoginOptions{}
}
