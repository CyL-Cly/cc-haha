//go:build darwin

package sourcecollation

import (
	"fmt"
	"github.com/ebitengine/purego"
)

// macOS's ICU is available through the dyld shared cache even when no standalone
// dylib exists on the filesystem. Every API list owns its collator reference.
func New() (*Comparator, error) {
	library, err := purego.Dlopen("/usr/lib/libicucore.A.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("Source ICU collation unavailable: %w", err)
	}
	unload := func() { purego.Dlclose(library) }
	result, err := bindComparator(func(name string) (uintptr, error) { return purego.Dlsym(library, name) }, unload, "")
	if err != nil {
		unload()
	}
	return result, err
}
