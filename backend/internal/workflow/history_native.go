package workflow

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const osSymlink = fs.ModeSymlink

func readLimited(reader io.Reader, max int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(reader, max+1))
}
func NativeHistoryFiles() HistoryFiles {
	return HistoryFiles{ReadDir: os.ReadDir, ReadFile: os.ReadFile, Stat: os.Stat, Lstat: os.Lstat, Realpath: filepath.EvalSymlinks, OpenNoFollow: openTaskOutput}
}
