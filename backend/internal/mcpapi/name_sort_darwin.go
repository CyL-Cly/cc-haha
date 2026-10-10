//go:build darwin

package mcpapi

import (
	"fmt"
	"github.com/ebitengine/purego"
)

// macOS's ICU is available through the dyld shared cache even when no standalone
// dylib exists on the filesystem. Every API list owns its collator reference.
func newNameComparator() (*nameComparator, error) {
	library, err := purego.Dlopen("/usr/lib/libicucore.A.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("MCP API ICU collation unavailable: %w", err)
	}
	unload := func() { purego.Dlclose(library) }
	result, err := bindNameComparator(func(name string) (uintptr, error) { return purego.Dlsym(library, name) }, unload, "")
	if err != nil {
		unload()
	}
	return result, err
}
