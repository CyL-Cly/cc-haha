//go:build darwin

package tui

import "golang.org/x/sys/unix"

func settleTerminalForObservation(fd uintptr) (int, error) {
	// XNU makes PENDIN sticky when ICANON is restored. FIONREAD asks the
	// kernel to process its pending-input state without reading any bytes.
	// Keep both raw snapshots; do not mask PENDIN or mutate configuration.
	// https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/tty.c
	// ttioctl_locked(FIONREAD) → ttnread → ttypend.
	// x/sys does not export FIONREAD on Darwin. Apple's sys/filio.h defines
	// it as _IOR('f', 127, int), the same value for darwin amd64/arm64.
	const fionread = 0x4004667f
	return unix.IoctlGetInt(int(fd), fionread)
}
