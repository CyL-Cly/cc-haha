//go:build windows

package cliinstallation

import "golang.org/x/sys/windows"

func processRunning(pid int) bool {
	if pid <= 1 {
		return false
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(process)
	var code uint32
	return windows.GetExitCodeProcess(process, &code) == nil && code == 259
}
