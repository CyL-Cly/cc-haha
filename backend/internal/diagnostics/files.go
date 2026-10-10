package diagnostics

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type OSFileSystem struct{}

func (OSFileSystem) MkdirAll(path string, mode fs.FileMode) error {
	err := os.MkdirAll(path, mode)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	// Bun recursive mkdir reports EEXIST for an already-existing regular leaf.
	// Source fsOperations swallows that error and lets its caller lstat the
	// leaf. Go reports ENOTDIR, so verify that exact existing-leaf case using
	// native metadata. A file in an intermediate parent still returns its error.
	if errors.Is(err, syscall.ENOTDIR) {
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode().IsRegular() {
			return nil
		}
	}
	return err
}
func (OSFileSystem) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }
func (OSFileSystem) Stat(path string) (fs.FileInfo, error)  { return os.Stat(path) }
func (OSFileSystem) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
func (OSFileSystem) Chmod(path string, mode fs.FileMode) error { return os.Chmod(path, mode) }
func (OSFileSystem) Rename(from, to string) error              { return os.Rename(from, to) }
func (OSFileSystem) Remove(path string) error                  { return os.Remove(path) }
func (OSFileSystem) ReadDir(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = string(protocol.DecodeJavaScriptUTF8([]byte(entry.Name())))
	}
	return names, nil
}
func (OSFileSystem) Append(path string, data []byte, mode fs.FileMode) error {
	// Source's explicit-mode append first tries exclusive creation. Only an
	// existing leaf permits the second open; POSIX must not follow that leaf.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_APPEND|os.O_WRONLY, mode)
	if err == nil {
		return appendAndClose(file, data)
	}
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY|appendNoFollowFlag, 0)
	if err != nil {
		return err
	}
	info, operationErr := file.Stat()
	if operationErr == nil && !info.Mode().IsRegular() {
		operationErr = errors.New("refusing non-regular append target")
	}
	if operationErr == nil && runtime.GOOS != "windows" {
		operationErr = file.Chmod(mode)
	}
	if operationErr != nil {
		if closeErr := file.Close(); closeErr != nil {
			return closeErr
		}
		return operationErr
	}
	return appendAndClose(file, data)
}

func appendAndClose(file *os.File, data []byte) error {
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if closeErr != nil {
		return closeErr // Source finally-close overrides an earlier write error.
	}
	return writeErr
}
