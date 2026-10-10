//go:build windows

package queryhooks

func hookProcessAlive(pid int) bool { return false }
