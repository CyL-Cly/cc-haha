//go:build windows

package builtintools

import "os"

func openTaskOutput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0666)
}
