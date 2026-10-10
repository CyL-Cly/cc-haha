//go:build !windows

package clibootstrap

import (
	"os"
	"syscall"
)

func writeNativeWorkflowOutput(path string, data []byte) error {
	file, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0666)
	if e != nil {
		return e
	}
	_, e = file.Write(data)
	closeError := file.Close()
	if e != nil {
		return e
	}
	return closeError
}
