//go:build windows

package diagnosticsapi

import "golang.org/x/sys/windows"

const noFollowFlag = 0
const directoryFlags = 0

func pidLiveness(pid int) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if err == windows.ERROR_INVALID_PARAMETER {
			return "dead"
		}
		if err == windows.ERROR_ACCESS_DENIED {
			return "alive"
		}
		return "unknown"
	}
	defer windows.CloseHandle(handle)
	var exit uint32
	if windows.GetExitCodeProcess(handle, &exit) != nil {
		return "unknown"
	}
	if exit == 259 {
		return "alive"
	}
	return "dead"
}
