package localindex

import (
	"os"
	"path/filepath"
	"strings"
)

func unsafePath() error { return coded("LOCAL_INDEX_UNSAFE_PATH", "LOCAL_INDEX_UNSAFE_PATH") }
func contained(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
func directory(path, trustRoot string) error {
	if err := os.Mkdir(path, 0777); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return unsafePath()
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if !contained(trustRoot, real) {
		return unsafePath()
	}
	return nil
}
func PrepareManagedDatabasePath(path, scope, name string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if scope == "" {
		if err = os.MkdirAll(filepath.Dir(absolute), 0777); err != nil {
			return err
		}
	} else {
		if !filepath.IsAbs(scope) {
			return unsafePath()
		}
		scope = filepath.Clean(scope)
		if absolute != filepath.Join(scope, "cc-haha", "db", name) {
			return unsafePath()
		}
		if err = os.MkdirAll(scope, 0777); err != nil {
			return err
		}
		real, err := filepath.EvalSymlinks(scope)
		if err != nil {
			return err
		}
		info, err := os.Lstat(scope)
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return unsafePath()
		}
		for _, child := range []string{filepath.Join(scope, "cc-haha"), filepath.Join(scope, "cc-haha", "db")} {
			if err = directory(child, real); err != nil {
				return err
			}
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		info, err := os.Lstat(absolute + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return unsafePath()
		}
		n, err := pathLinkCount(absolute+suffix, info)
		if err != nil {
			return err
		}
		if n != 1 {
			return unsafePath()
		}
	}
	return nil
}
