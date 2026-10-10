//go:build darwin || linux

package imageprocessing

import "github.com/ebitengine/purego"

func openNativeCodec(path string) (Codec, error) { return OpenVips(path) }
func openVipsLibrary(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}
func lookupVipsSymbol(handle uintptr, name string) (uintptr, error) {
	return purego.Dlsym(handle, name)
}
func closeVipsLibrary(handle uintptr) error { return purego.Dlclose(handle) }
