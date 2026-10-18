package clibootstrap

import (
	"io/fs"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"golang.org/x/text/unicode/norm"
)

// Only the cross-client user root follows current HOME. Project traversal
// continues to use the original process home, as Bun's homedir does.
func (n *NativeBoundary) nativeSkillHomeDirectory() string {
	key := "HOME"
	if n.platform == "windows" {
		key = "USERPROFILE"
	}
	home, present := n.nativeEnvironmentValue(key)
	if !present || home == "" {
		home = n.Home
	}
	home = norm.NFC.String(home)
	if n.platform == "windows" {
		return nativepaths.ResolveWindows(home, n.CWD)
	}
	if !filepath.IsAbs(home) {
		home = filepath.Join(n.CWD, home)
	}
	return home
}

// Read the original requested path against this native process's CWD, keeping
// raw path spelling in the command's Source metadata.
type nativeSkillFiles struct{ n *NativeBoundary }

func (f nativeSkillFiles) Stat(path string) (fs.FileInfo, error) {
	return (skillcommands.NativeFiles{}).Stat(f.n.nativePhysicalPath(path))
}
func (f nativeSkillFiles) Lstat(path string) (fs.FileInfo, error) {
	return (skillcommands.NativeFiles{}).Lstat(f.n.nativePhysicalPath(path))
}
func (f nativeSkillFiles) ReadDir(path string) ([]fs.DirEntry, error) {
	return (skillcommands.NativeFiles{}).ReadDir(f.n.nativePhysicalPath(path))
}
func (f nativeSkillFiles) ReadFile(path string) ([]byte, error) {
	return (skillcommands.NativeFiles{}).ReadFile(f.n.nativePhysicalPath(path))
}
func (f nativeSkillFiles) Realpath(path string) (string, error) {
	return (skillcommands.NativeFiles{}).Realpath(f.n.nativePhysicalPath(path))
}
