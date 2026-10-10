//go:build windows

package pluginzip

import (
	"errors"
	"os"
	"strconv"
	"syscall"
)

func directoryIdentity(path string, info os.FileInfo) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	var native syscall.ByHandleFileInformation
	if syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &native) != nil {
		return ""
	}
	dev, ino := uint64(native.VolumeSerialNumber), uint64(native.FileIndexHigh)<<32|uint64(native.FileIndexLow)
	if dev == 0 && ino == 0 {
		return ""
	}
	return strconv.FormatUint(dev, 10) + ":" + strconv.FormatUint(ino, 10)
}
func unixMode(info os.FileInfo) uint16 { return uint16(info.Mode().Perm()) | 0100000 }
func platformIOError(err error) (string, string) {
	const errorDirectory syscall.Errno = 267
	if errors.Is(err, errorDirectory) {
		return "ENOTDIR", "not a directory"
	}
	if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		return "EACCES", "permission denied"
	}
	return "", ""
}
