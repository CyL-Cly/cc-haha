//go:build windows

package sessionrewind

import (
	"os"
	"syscall"
)

func openNoFollow(path string, flags int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flags, mode)
}
func fileLinkCount(file *os.File, _ os.FileInfo) (uint64, error) {
	var info syscall.ByHandleFileInformation
	err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info)
	return uint64(info.NumberOfLinks), err
}
func pathLinkCount(path string, info os.FileInfo) (uint64, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, ErrUnsafeCheckpointFile
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !os.SameFile(info, actual) {
		return 0, ErrUnsafeCheckpointFile
	}
	return fileLinkCount(file, actual)
}

func writableAccess(path string) error {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attrs, err := syscall.GetFileAttributes(ptr)
	if err != nil {
		return err
	}
	if attrs&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 && attrs&syscall.FILE_ATTRIBUTE_READONLY != 0 {
		return syscall.EACCES
	}
	return nil
}
