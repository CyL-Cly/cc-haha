//go:build windows

package server

import (
	"errors"
	"golang.org/x/sys/windows"
)

func migrationProcessAlive(pid int) bool {
	if pid > 2147483647 {
		return true
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if windows.GetExitCodeProcess(handle, &exitCode) != nil {
		return true
	}
	return exitCode == 259 // STILL_ACTIVE
}
