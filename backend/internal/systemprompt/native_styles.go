package systemprompt

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"io/fs"
)

// ResolveWithNativeFiles keeps this loader's all/directory Promise caches and
// settings selection. Only OS calls and observing a pending cache are adapted
// to the caller's existing Owner continuation.
func (l *StyleLoader) ResolveWithNativeFiles(ctx context.Context, cwd string, files usercontext.MemoryFiles, readDir func(string) ([]fs.DirEntry, error), realpath func(string) (string, error), lstat func(string) (fs.FileInfo, error), wait func(<-chan struct{}) error) (*OutputStyle, error) {
	if files == nil || readDir == nil || realpath == nil || lstat == nil || wait == nil {
		return nil, &usercontext.CapabilityError{Boundary: "native output style filesystem"}
	}
	o := l.options
	o.Files, o.ReadDir, o.Realpath, o.Lstat = files, readDir, realpath, lstat
	return l.resolveWithOptions(ctx, cwd, o, wait)
}
