package diagnosticsapi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
func (s *Service) ensure() error {
	if err := os.MkdirAll(s.options.ConfigDir, 0700); err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(s.options.ConfigDir, "cc-haha"), s.LogDir(), s.ExportDir()} {
		if err := s.ensureDirectory(path); err != nil {
			return err
		}
	}
	for _, directory := range []string{s.LogDir(), s.ExportDir()} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			path := filepath.Join(directory, entry.Name())
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("Refusing symbolic link in diagnostics directory: %s", path)
			}
			if info.Mode().IsRegular() {
				file, err := s.openFile(path, os.O_RDONLY)
				if err != nil {
					return err
				}
				if runtime.GOOS != "windows" {
					err = file.Chmod(0600)
				}
				closeErr := file.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
		}
	}
	return nil
}
func (s *Service) ensureDirectory(path string) error {
	if !inside(s.options.ConfigDir, path) {
		return errors.New("Refusing diagnostics directory outside the configured root")
	}
	err := os.Mkdir(path, 0700)
	if err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Refusing symbolic link for diagnostics directory: %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("Refusing non-directory diagnostics path: %s", path)
	}
	root, err := filepath.EvalSymlinks(s.options.ConfigDir)
	if err != nil {
		return err
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if !inside(root, actual) {
		return fmt.Errorf("Refusing diagnostics directory outside the configured root: %s", path)
	}
	file, err := os.OpenFile(path, os.O_RDONLY|directoryFlags, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	if runtime.GOOS != "windows" {
		return file.Chmod(0700)
	}
	return nil
}
func (s *Service) openFile(path string, flags int) (*os.File, error) {
	root, err := filepath.EvalSymlinks(s.options.ConfigDir)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if !inside(root, parent) {
		return nil, fmt.Errorf("Refusing diagnostics file outside the configured root: %s", path)
	}
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("Refusing symbolic link for diagnostics file: %s", path)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Refusing non-regular diagnostics file: %s", path)
		}
	}
	file, err := os.OpenFile(path, flags|noFollowFlag, 0600)
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("Refusing non-regular diagnostics file: %s", path)
	}
	return file, nil
}
func (s *Service) writeFile(path string, data []byte, appendMode bool) error {
	flags := os.O_WRONLY | os.O_CREATE
	if appendMode {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	file, err := s.openFile(path, flags)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		err = file.Chmod(0600)
	}
	if err == nil {
		_, err = file.Write(data)
	}
	closeErr := file.Close()
	if closeErr != nil {
		return closeErr
	}
	return err
}
func (s *Service) atomicWrite(path string, data []byte) error {
	id, err := identifier()
	if err != nil {
		return err
	}
	temporary := path + "." + id + ".tmp"
	defer os.Remove(temporary)
	if err = s.writeFile(temporary, data, false); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

type fileMetadata struct {
	Path    string
	Size    int64
	ModTime time.Time
}

func (s *Service) listFiles(directory string) ([]fileMetadata, error) {
	result := []fileMetadata{}
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		if entry.IsDir() {
			nested, err := s.listFiles(path)
			if err != nil {
				return nil, err
			}
			result = append(result, nested...)
		} else if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			result = append(result, fileMetadata{path, info.Size(), info.ModTime()})
		}
	}
	return result, nil
}
