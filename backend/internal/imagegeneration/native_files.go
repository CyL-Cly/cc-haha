package imagegeneration

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
)

type NativeFiles struct{ Check func(string, string) error }

func (f NativeFiles) check(op, path string) error {
	if f.Check != nil {
		return f.Check(op, path)
	}
	return nil
}
func (f NativeFiles) Realpath(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := f.check("image-realpath", path); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}
func (f NativeFiles) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.check("image-stat", path); err != nil {
		return nil, err
	}
	return os.Stat(path)
}
func (f NativeFiles) ReadNoFollow(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.check("image-read", path); err != nil {
		return nil, err
	}
	file, err := openImageNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readImageFile(file)
}
func (f NativeFiles) MkdirAll(ctx context.Context, path string, mode fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.check("image-mkdir", path); err != nil {
		return err
	}
	return os.MkdirAll(path, mode)
}
func (f NativeFiles) Chmod(ctx context.Context, path string, mode fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.check("image-chmod", path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
func (f NativeFiles) WriteExclusive(ctx context.Context, path string, data []byte, mode fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.check("image-write-exclusive", path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeError := file.Write(data)
	closeError := file.Close()
	if writeError != nil {
		return writeError
	}
	return closeError
}
