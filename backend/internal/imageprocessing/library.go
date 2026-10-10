package imageprocessing

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

var libraryCodecs sync.Map
var libraryCodecLoad sync.Mutex

func BundledCodec(library string) (Codec, error) {
	if library == "" {
		var err error
		library, err = FindBundledLibrary()
		if err != nil {
			return nil, err
		}
	}
	if cached, ok := libraryCodecs.Load(library); ok {
		return cached.(Codec), nil
	}
	// Source's module import initializes the shared native library once.
	// LoadOrStore alone is too late: concurrent cold calls would both enter
	// libvips initialization before either codec is published.
	libraryCodecLoad.Lock()
	defer libraryCodecLoad.Unlock()
	if cached, ok := libraryCodecs.Load(library); ok {
		return cached.(Codec), nil
	}
	codec, err := openNativeCodec(library)
	if err != nil {
		return nil, err
	}
	actual, _ := libraryCodecs.LoadOrStore(library, codec)
	return actual.(Codec), nil
}

// Mirrors compiled Source dependency resolution beside the executable's
// ancestors. The current project directory and ambient user HOME are not
// searched for a native executable library.
func FindBundledLibrary() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	platform := runtime.GOOS
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	packages := []string{"sharp-libvips-" + platform + "-" + arch}
	patterns := []string{"libvips-cpp.*"}
	if platform == "windows" {
		packages = []string{"sharp-win32-" + arch, "sharp-libvips-win32-" + arch}
		patterns = []string{"libvips-*.dll"}
	}
	if platform == "linux" {
		packages = append(packages, "sharp-libvips-linuxmusl-"+arch)
	}
	for directory := filepath.Dir(executable); ; directory = filepath.Dir(directory) {
		for _, base := range []string{directory, filepath.Join(directory, "app.asar.unpacked")} {
			for _, pkg := range packages {
				for _, library := range patterns {
					pattern := filepath.Join(base, "node_modules", "@img", pkg, "lib", library)
					paths, _ := filepath.Glob(pattern)
					for _, path := range paths {
						if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
							return path, nil
						}
					}
				}
			}
		}
		if parent := filepath.Dir(directory); parent == directory {
			break
		}
	}
	return "", errors.New("Native image processor module not available")
}
