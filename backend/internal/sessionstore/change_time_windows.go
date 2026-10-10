//go:build windows

package sessionstore

import (
	"math/big"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func fileChangeTimeNS(file *os.File, _ os.FileInfo) (string, error) {
	var basic struct {
		CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
		FileAttributes                                          uint32
		_                                                       uint32
	}
	if err := windows.GetFileInformationByHandleEx(windows.Handle(file.Fd()), windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		return "", err
	}
	ticks := new(big.Int).Sub(big.NewInt(basic.ChangeTime), big.NewInt(116444736000000000))
	return ticks.Mul(ticks, big.NewInt(100)).String(), nil
}
