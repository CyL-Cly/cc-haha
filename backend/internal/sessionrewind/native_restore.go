package sessionrewind

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/checkpointfiles"
	"os"
	"path/filepath"
)

type FileState = checkpointfiles.FileState

type RestoreFiles interface {
	ReadCurrentState(context.Context, string) (FileState, error)
	ReadBackupState(context.Context, string, string) (FileState, error)
	AssertWritable(context.Context, string, FileState, FileState) error
	WriteState(context.Context, string, FileState) error
}

func (NativeFiles) ReadCurrentState(ctx context.Context, path string) (FileState, error) {
	if err := ctx.Err(); err != nil {
		return FileState{}, err
	}
	file, err := openNoFollow(path, os.O_RDONLY, 0)
	if os.IsNotExist(err) {
		return FileState{}, nil
	}
	if err != nil {
		return FileState{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return FileState{}, err
	}
	links, err := fileLinkCount(file, info)
	if err != nil {
		return FileState{}, err
	}
	if !safeFile(info, links) {
		return FileState{}, &BadRequest{"File cannot be restored safely: " + path}
	}
	content, err := readOpened(ctx, file)
	if err != nil {
		return FileState{}, err
	}
	return FileState{Exists: true, Content: content, Mode: info.Mode()}, nil
}

func (NativeFiles) AssertWritable(ctx context.Context, path string, original, target FileState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if original.Exists && target.Exists {
		file, err := openNoFollow(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		links, err := fileLinkCount(file, info)
		if err != nil {
			return err
		}
		if !safeFile(info, links) {
			return &BadRequest{"File cannot be restored safely: " + path}
		}
		return nil
	}
	parent := filepath.Dir(path)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := writableAccess(parent)
		if err == nil {
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return err
		}
		parent = next
	}
}

func (files NativeFiles) WriteState(ctx context.Context, path string, state FileState) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	if !state.Exists {
		current, err := files.ReadCurrentState(ctx, path)
		if err != nil {
			return err
		}
		if current.Exists {
			err = os.Remove(path)
		}
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	file, err := openNoFollow(path, os.O_WRONLY, 0)
	if os.IsNotExist(err) {
		if err = os.MkdirAll(filepath.Dir(path), 0777); err != nil {
			return err
		}
		file, err = openNoFollow(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, state.Mode)
	}
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	links, err := fileLinkCount(file, info)
	if err != nil {
		return err
	}
	if !safeFile(info, links) {
		return &BadRequest{"File cannot be restored safely: " + path}
	}
	if err = file.Truncate(0); err != nil {
		return err
	}
	content := state.Content
	for len(content) > 0 {
		if err = ctx.Err(); err != nil {
			return err
		}
		count, writeErr := file.Write(content)
		if writeErr != nil {
			return writeErr
		}
		content = content[count:]
	}
	return file.Chmod(state.Mode)
}
