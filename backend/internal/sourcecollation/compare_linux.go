//go:build linux

package sourcecollation

import (
	"fmt"
	"github.com/ebitengine/purego"
)

// Distribution ICU names and renamed C symbols include their ABI major. Probe
// the actual native image/symbols, with no shell/ldconfig or business worker.
func New() (*Comparator, error) {
	names := []string{"libicui18n.so"}
	for major := 100; major >= 40; major-- {
		names = append(names, fmt.Sprintf("libicui18n.so.%d", major))
	}
	for _, name := range names {
		library, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			continue
		}
		unload := func() { purego.Dlclose(library) }
		lookup := func(name string) (uintptr, error) { return purego.Dlsym(library, name) }
		suffixes := []string{""}
		for major := 100; major >= 40; major-- {
			suffixes = append(suffixes, fmt.Sprintf("_%d", major))
		}
		for _, suffix := range suffixes {
			if result, err := bindComparator(lookup, unload, suffix); err == nil {
				return result, nil
			}
		}
		unload()
	}
	// A minimal installation must still show all configured connectors. This
	// generic compatibility comparator never drops/rejects names; its ICU-table
	// differences are recorded as a platform parity gap, not an exact Source claim.
	return compatibleComparator(), nil
}
