//go:build windows

package clibootstrap

import (
	"io/fs"
	"syscall"
)

func agentsFileIdentity(i fs.FileInfo) string {
	if value, ok := i.(agentsIdentityInfo); ok {
		return value.identity
	}
	return ""
}
func withAgentsNativeIdentity(path string, info fs.FileInfo, nofollow bool) fs.FileInfo {
	return agentsInfoWithIdentity(path, info, nofollow, probeAgentsWindowsIdentity)
}
func probeAgentsWindowsIdentity(path string, nofollow bool) (uint64, uint64, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	flags := uint32(syscall.FILE_FLAG_BACKUP_SEMANTICS)
	if nofollow {
		flags |= syscall.FILE_FLAG_OPEN_REPARSE_POINT
	}
	handle, err := syscall.CreateFile(name, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, flags, 0)
	if err != nil {
		return 0, 0, err
	}
	defer syscall.CloseHandle(handle)
	var data syscall.ByHandleFileInformation
	if err = syscall.GetFileInformationByHandle(handle, &data); err != nil {
		return 0, 0, err
	}
	// libuv's Windows stat producer reports the volume serial's low 32 bits
	// and FILE_INTERNAL_INFORMATION's 64-bit file index, not a 128-bit FileId.
	return uint64(data.VolumeSerialNumber), uint64(data.FileIndexHigh)<<32 | uint64(data.FileIndexLow), nil
}
