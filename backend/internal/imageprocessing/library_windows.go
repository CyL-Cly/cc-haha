//go:build windows

package imageprocessing

import (
	"golang.org/x/sys/windows"
	"path/filepath"
)

func openNativeCodec(path string) (Codec, error) { return OpenVips(path) }
func openVipsLibrary(path string) (uintptr, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.LoadLibraryEx(absolute, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_DEFAULT_DIRS)
	return uintptr(handle), err
}
func lookupVipsSymbol(handle uintptr, name string) (uintptr, error) {
	return windows.GetProcAddress(windows.Handle(handle), name)
}
func closeVipsLibrary(handle uintptr) error { return windows.FreeLibrary(windows.Handle(handle)) }
