//go:build !windows

package chromenative

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func prepareSocketDirectory(directory string) error {
	if info, err := os.Stat(directory); err == nil && !info.IsDir() {
		if err := os.Remove(directory); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	_ = os.Chmod(directory, 0700)
	if entries, err := os.ReadDir(directory); err == nil {
		for _, entry := range entries {
			if pid, ok := legacyPID(entry.Name()); ok && syscall.Kill(pid, 0) != nil {
				_ = os.Remove(filepath.Join(directory, entry.Name()))
			}
		}
	}
	return nil
}
func listenSocket(path string) (net.Listener, error) { return net.Listen("unix", path) }
