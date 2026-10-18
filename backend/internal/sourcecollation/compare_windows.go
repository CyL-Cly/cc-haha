//go:build windows

package sourcecollation

import "golang.org/x/sys/windows"

// Windows 10 1903+ exposes ICU's stable C ABI in System32\icu.dll. Restrict DLL
// loading to System32 so a workspace/PATH DLL cannot replace this OS primitive.
func New() (*Comparator, error) {
	library, err := windows.LoadLibraryEx("icu.dll", 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err == nil {
		unload := func() { windows.FreeLibrary(library) }
		result, err := bindComparator(func(name string) (uintptr, error) { return windows.GetProcAddress(library, name) }, unload, "")
		if err == nil {
			return result, nil
		}
		unload()
	}
	// Older hosts retain every connector using the same generic compatibility
	// path. Actual Windows ICU execution and fallback parity remain separately
	// measured; native availability is not inferred from a successful build.
	return compatibleComparator(), nil
}
