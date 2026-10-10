//go:build !windows

package diagnostics

import "syscall"

const appendNoFollowFlag = syscall.O_NOFOLLOW
