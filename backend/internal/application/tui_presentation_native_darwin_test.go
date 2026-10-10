//go:build darwin

package application

import "golang.org/x/sys/unix"

func nativeTUIPresentationTerminalProbe(fd uintptr) (int, error) {
	// XNU ttypend processes the native pending-input marker when FIONREAD is
	// observed, without reading/discarding bytes or masking terminal flags.
	const fionread = 0x4004667f
	return unix.IoctlGetInt(int(fd), fionread)
}
