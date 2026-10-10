//go:build windows

package voice

import (
	"golang.org/x/sys/windows"
)

func openNativeLibrary(path string) (uintptr, error) {
	handle, e := windows.LoadLibrary(path)
	return uintptr(handle), e
}
func lookupNativeSymbol(handle uintptr, name string) (uintptr, error) {
	return windows.GetProcAddress(windows.Handle(handle), name)
}
func closeNativeLibrary(handle uintptr) error { return windows.FreeLibrary(windows.Handle(handle)) }
