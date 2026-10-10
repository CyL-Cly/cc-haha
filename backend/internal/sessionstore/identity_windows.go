//go:build windows

package sessionstore

import (
	"os"
	"strconv"
	"syscall"
)

func fileIdentity(file *os.File, _ os.FileInfo) (string, string, error) {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return "", "", err
	}
	return strconv.FormatUint(uint64(info.VolumeSerialNumber), 10), strconv.FormatUint(uint64(info.FileIndexHigh)<<32|uint64(info.FileIndexLow), 10), nil
}
