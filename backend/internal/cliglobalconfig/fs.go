package cliglobalconfig

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type OSFileSystem struct{}

func (OSFileSystem) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (OSFileSystem) Stat(path string) (FileStat, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return FileStat{}, err
	}
	return FileStat{Mtime: float64(stat.ModTime().UnixNano()) / 1e6, Size: stat.Size(), Mode: stat.Mode()}, nil
}
func (OSFileSystem) Exists(path string) bool    { _, err := os.Stat(path); return err == nil }
func (OSFileSystem) MkdirAll(path string) error { return os.MkdirAll(path, 0777) }
func (OSFileSystem) Mkdir(path string) error    { return os.Mkdir(path, 0777) }
func (OSFileSystem) Remove(path string) error   { return os.Remove(path) }
func (OSFileSystem) ReadDir(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names, nil
}
func (OSFileSystem) Readlink(path string) (string, error)      { return os.Readlink(path) }
func (OSFileSystem) Realpath(path string) (string, error)      { return filepath.EvalSymlinks(path) }
func (OSFileSystem) Chmod(path string, mode fs.FileMode) error { return os.Chmod(path, mode) }
func (OSFileSystem) Chtimes(path string, stamp time.Time) error {
	return os.Chtimes(path, stamp, stamp)
}
func (OSFileSystem) Rename(source, target string) error { return os.Rename(source, target) }
func (OSFileSystem) WriteFlush(path string, data []byte, mode *fs.FileMode) (err error) {
	permissions := fs.FileMode(0666)
	if mode != nil {
		permissions = *mode
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, permissions)
	if err != nil {
		return err
	}
	defer func() {
		closed := file.Close()
		if err == nil {
			err = closed
		}
	}()
	if _, err = file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}
func (OSFileSystem) CopyFile(source, target string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	stat, err := input.Stat()
	if err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, stat.Mode())
	if err != nil {
		return err
	}
	defer func() {
		closed := output.Close()
		if err == nil {
			err = closed
		}
	}()
	_, err = io.Copy(output, input)
	return err
}
