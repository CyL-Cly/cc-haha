//go:build windows

package imagegeneration

import (
	"io"
	"os"
)

// Original Windows fsConstants.O_NOFOLLOW is absent.
func openImageNoFollow(path string) (*os.File, error) { return os.OpenFile(path, os.O_RDONLY, 0) }
func readImageFile(file *os.File) ([]byte, error)     { return io.ReadAll(file) }
