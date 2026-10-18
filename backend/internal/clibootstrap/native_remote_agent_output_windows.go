//go:build windows

package clibootstrap

import "os"

func openNativeRemoteAgentOutput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0666)
}
