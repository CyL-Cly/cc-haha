//go:build windows

package windowspaths

import "path/filepath"

func windowsDriveDirectory(drive string) string {
	// Windows maintains a current directory for every drive. Resolve through
	// the actual OS, as Node's win32.resolve does for a drive-relative path.
	if path, err := filepath.Abs(drive + "."); err == nil {
		return path
	}
	return drive + `\`
}
