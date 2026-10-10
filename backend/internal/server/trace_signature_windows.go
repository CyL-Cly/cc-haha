//go:build windows

package server

import (
	"golang.org/x/sys/windows"
	"os"
	"strconv"
	"unsafe"
)

func traceCTime(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	var basic struct {
		CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
		Attributes                                              uint32
		Padding                                                 uint32
	}
	e = windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic)))
	if e != nil {
		return "", e
	}
	return strconv.FormatInt((basic.ChangeTime-116444736000000000)*100, 10), nil
}
