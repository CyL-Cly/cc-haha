//go:build !windows

package application

import "syscall"

// publicPTYProcessSignalZero preserves the original Unix signal-zero receipt.
// It probes only the actual PID recorded by the private PTY peer.
func publicPTYProcessSignalZero(pid int) error {
	return syscall.Kill(pid, 0)
}
