//go:build windows

package clibootstrap

import "os"

func writeNativeWorkflowOutput(path string, data []byte) error { return os.WriteFile(path, data, 0666) }
