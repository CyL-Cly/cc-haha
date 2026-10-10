//go:build !darwin && !linux && !windows

package voice

import (
	"errors"
)

func openNativeLibrary(string) (uintptr, error) {
	return 0, errors.New("Local speech runtime is not supported on this platform")
}
func lookupNativeSymbol(uintptr, string) (uintptr, error) {
	return 0, errors.New("Local speech runtime is not supported on this platform")
}
func closeNativeLibrary(uintptr) error { return nil }
