//go:build windows

package localindex

import (
	"golang.org/x/sys/windows"
	"os"
	"syscall"
	"unsafe"
)

func pathLinkCount(path string, info os.FileInfo) (uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !os.SameFile(info, current) {
		return 0, unsafePath()
	}
	var actual syscall.ByHandleFileInformation
	err = syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &actual)
	return uint64(actual.NumberOfLinks), err
}
func snapshot(info os.FileInfo) (FileSnapshot, error) {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return FileSnapshot{}, unsafePath()
	}
	mtime := float64(info.ModTime().Unix())*1000 + float64(info.ModTime().Nanosecond())/1e6
	ctime := float64(data.CreationTime.Nanoseconds()) / 1e6
	return FileSnapshot{info.Size(), mtime, ctime, nil}, nil
}
func nativeFileSnapshot(file *os.File, info os.FileInfo) (FileSnapshot, error) {
	s, err := snapshot(info)
	if err != nil {
		return s, err
	}
	var basic struct {
		CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
		Attributes                                              uint32
		Padding                                                 uint32
	}
	err = windows.GetFileInformationByHandleEx(windows.Handle(file.Fd()), windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic)))
	if err != nil {
		return FileSnapshot{}, err
	}
	ticks := basic.ChangeTime - 116444736000000000
	s.CtimeMs = float64(ticks/10000000)*1000 + float64(ticks%10000000)/10000
	return s, nil
}
