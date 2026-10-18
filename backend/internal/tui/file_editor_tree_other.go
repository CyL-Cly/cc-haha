//go:build !windows && !darwin && !linux

package tui

import "errors"

func readFileEditorProcess(int) (fileEditorProcessIdentity, bool, error) {
	return fileEditorProcessIdentity{}, false, errors.New("editor cancellation has no stable process identity reader on this platform")
}
func snapshotFileEditorProcesses() ([]fileEditorProcessIdentity, error) {
	return nil, errors.New("editor cancellation has no stable descendant reader on this platform")
}
