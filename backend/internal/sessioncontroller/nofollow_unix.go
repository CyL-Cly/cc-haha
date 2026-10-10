//go:build darwin || linux

package sessioncontroller

import "syscall"

func noFollowFlag() int { return syscall.O_NOFOLLOW }
