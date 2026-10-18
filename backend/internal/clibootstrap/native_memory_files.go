package clibootstrap

import (
	"io/fs"

	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

// Memory metadata retains the original requested path. Only the actual native
// filesystem operation resolves a relative path against the process CWD.
// Finite boundaries still reject relative paths through their original guard.
type nativeMemoryFiles struct{ n *NativeBoundary }

func (f nativeMemoryFiles) ReadFile(path string) ([]byte, error) {
	return (mainSettingsFiles{n: f.n}).ReadFile(f.n.nativePhysicalPath(path))
}
func (f nativeMemoryFiles) Stat(path string) (fs.FileInfo, error) {
	return (mainSettingsFiles{n: f.n}).Stat(f.n.nativePhysicalPath(path))
}
func (f nativeMemoryFiles) Realpath(path string) (string, error) {
	return (mainSettingsFiles{n: f.n}).EvalSymlinks(f.n.nativePhysicalPath(path))
}
func (f nativeMemoryFiles) MkdirAll(path string) error {
	return (mainSettingsFiles{n: f.n}).MkdirAll(f.n.nativePhysicalPath(path))
}
func (f nativeMemoryFiles) ReadDir(path string) ([]fs.DirEntry, error) {
	physical := f.n.nativePhysicalPath(path)
	if err := f.n.check("readdir", physical); err != nil {
		return nil, err
	}
	return usercontext.NativeReadDir(physical)
}
