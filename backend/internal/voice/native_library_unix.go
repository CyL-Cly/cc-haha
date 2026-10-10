//go:build darwin || linux

package voice

import (
	"github.com/ebitengine/purego"
)

func openNativeLibrary(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}
func lookupNativeSymbol(handle uintptr, name string) (uintptr, error) {
	return purego.Dlsym(handle, name)
}
func closeNativeLibrary(handle uintptr) error { return purego.Dlclose(handle) }
