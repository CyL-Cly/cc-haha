package sdkhost_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func fail() error {
	return errors.New("Headless controller fixture requires disposable HOME/config and ordinary private files")
}
func inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
func assertHome(home string) error {
	if !filepath.IsAbs(home) || strings.ContainsRune(home, 0) || home != os.Getenv("HOME") || os.Getenv("USERPROFILE") != home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		return fail()
	}
	info, err := os.Lstat(home)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail()
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		return fail()
	}
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if !filepath.IsAbs(root) {
		return fail()
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil || canonical == canonicalRoot || !inside(canonicalRoot, canonical) {
		return fail()
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail()
	}
	canonicalCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil || canonicalCwd != canonical {
		return fail()
	}
	config := filepath.Join(home, ".claude")
	if info, err := os.Lstat(config); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail()
	}
	canonicalConfig, err := filepath.EvalSymlinks(config)
	if err != nil || !inside(canonical, canonicalConfig) {
		return fail()
	}
	return nil
}
func guard(home, path string, ordinary bool) error {
	if !inside(home, path) {
		return fail()
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return fail()
	}
	for probe := path; ; probe = filepath.Dir(probe) {
		info, err := os.Lstat(probe)
		if errors.Is(err, os.ErrNotExist) {
			if probe == filepath.Dir(probe) {
				return fail()
			}
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || probe == path && ordinary && !info.Mode().IsRegular() {
			return fail()
		}
		canonical, err := filepath.EvalSymlinks(probe)
		if err != nil || !inside(canonicalHome, canonical) {
			return fail()
		}
		return nil
	}
}
