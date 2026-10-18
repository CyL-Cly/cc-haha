//go:build windows

package gochildcoverage

import "os"

func InstallHangupSignal() error { return nil }

func Exit(code int) { os.Exit(code) }
