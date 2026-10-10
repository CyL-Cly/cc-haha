//go:build windows

package clibootstrap

import "io/fs"

// Windows identity producer remains unported; actual public native profile is guarded.
func agentsFileIdentity(i fs.FileInfo) string { return "" }
