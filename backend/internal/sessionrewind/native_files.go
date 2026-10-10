package sessionrewind

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// NativeFiles reads original checkpoint bytes from the explicit configuration
// directory. It does not discover a home or load an alternative transcript.
type NativeFiles struct{ ConfigHome string }

var ErrUnsafeCheckpointFile = errors.New("file checkpoint path is unsafe or changed while opening")

func safeSegment(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\\x00")
}
func canonicalExisting(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	segments := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(segments) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, segments[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		segments = append(segments, filepath.Base(path))
		path = parent
	}
}
func (NativeFiles) CanonicalPath(path string) string {
	value, err := canonicalExisting(path)
	if err == nil {
		return value
	}
	value, _ = filepath.Abs(path)
	return value
}
func pathsEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
func within(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	return err == nil && (rel == "." || !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}
func (NativeFiles) SafeTrackedPath(ctx context.Context, base, path string) bool {
	if ctx.Err() != nil {
		return false
	}
	base = absolute(base, ".")
	abs := absolute(base, path)
	if !filepath.IsAbs(path) && !within(base, abs) {
		return false
	}
	root := base
	for !within(root, abs) {
		parent := filepath.Dir(root)
		if parent == root {
			root = filepath.VolumeName(abs) + string(filepath.Separator)
			break
		}
		root = parent
	}
	realRoot, err := canonicalExisting(root)
	if err != nil {
		return false
	}
	realPath, err := canonicalExisting(abs)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || !pathsEqual(realPath, filepath.Join(realRoot, rel)) {
		return false
	}
	info, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	links, err := pathLinkCount(abs, info)
	return err == nil && links == 1
}
func safeFile(info os.FileInfo, links uint64) bool {
	return info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && links == 1
}
func readOpened(ctx context.Context, file *os.File) ([]byte, error) {
	result := []byte{}
	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := file.Read(buffer)
		result = append(result, buffer[:n]...)
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
	}
}
func (NativeFiles) ReadCurrent(ctx context.Context, path string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	file, err := openNoFollow(path, os.O_RDONLY, 0)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	links, err := fileLinkCount(file, info)
	if err != nil {
		return nil, false, err
	}
	if !safeFile(info, links) {
		return nil, false, ErrUnsafeCheckpointFile
	}
	data, err := readOpened(ctx, file)
	return data, true, err
}

type directoryIdentity struct {
	path string
	info os.FileInfo
}

func (n NativeFiles) backupDirectories(id string) ([]directoryIdentity, error) {
	if !filepath.IsAbs(n.ConfigHome) || !safeSegment(id) {
		return nil, ErrUnsafeCheckpointFile
	}
	result := []directoryIdentity{}
	path := n.ConfigHome
	for _, segment := range []string{"file-history", id} {
		path = filepath.Join(path, segment)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrUnsafeCheckpointFile
		}
		result = append(result, directoryIdentity{path, info})
	}
	return result, nil
}
func unchangedDirectories(entries []directoryIdentity) error {
	for _, entry := range entries {
		now, err := os.Lstat(entry.path)
		if err != nil {
			return err
		}
		if !now.IsDir() || now.Mode()&os.ModeSymlink != 0 || !os.SameFile(entry.info, now) {
			return ErrUnsafeCheckpointFile
		}
	}
	return nil
}
func severBackupLinks(ctx context.Context, path string, info os.FileInfo) (os.FileInfo, error) {
	source, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	actual, err := source.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, actual) {
		return nil, ErrUnsafeCheckpointFile
	}
	target, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return nil, err
	}
	temp := target.Name()
	defer os.Remove(temp)
	defer target.Close()
	buffer := make([]byte, 64*1024)
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			if _, err = target.Write(buffer[:count]); err != nil {
				return nil, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if err = target.Chmod(info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)); err != nil {
		return nil, err
	}
	if err = target.Sync(); err != nil {
		return nil, err
	}
	if err = target.Close(); err != nil {
		return nil, err
	}
	if err = source.Close(); err != nil {
		return nil, err
	}
	if err = os.Rename(temp, path); err != nil {
		return nil, err
	}
	return os.Lstat(path)
}
func (n NativeFiles) ReadBackup(ctx context.Context, id, name string) ([]byte, error) {
	state, err := n.ReadBackupState(ctx, id, name)
	return state.Content, err
}
func (n NativeFiles) ReadBackupState(ctx context.Context, id, name string) (FileState, error) {
	if err := ctx.Err(); err != nil {
		return FileState{}, err
	}
	if !safeSegment(name) {
		return FileState{}, ErrUnsafeCheckpointFile
	}
	dirs, err := n.backupDirectories(id)
	if err != nil {
		return FileState{}, err
	}
	path := filepath.Join(dirs[len(dirs)-1].path, name)
	info, err := os.Lstat(path)
	if err != nil {
		return FileState{}, err
	}
	links, err := pathLinkCount(path, info)
	if err != nil {
		return FileState{}, err
	}
	if info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && links > 1 {
		info, err = severBackupLinks(ctx, path, info)
		if err != nil {
			return FileState{}, err
		}
		links, err = pathLinkCount(path, info)
		if err != nil {
			return FileState{}, err
		}
	}
	if !safeFile(info, links) {
		return FileState{}, ErrUnsafeCheckpointFile
	}
	file, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return FileState{}, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return FileState{}, err
	}
	links, err = fileLinkCount(file, actual)
	if err != nil {
		return FileState{}, err
	}
	if !safeFile(actual, links) || !os.SameFile(info, actual) {
		return FileState{}, ErrUnsafeCheckpointFile
	}
	if err = unchangedDirectories(dirs); err != nil {
		return FileState{}, err
	}
	content, err := readOpened(ctx, file)
	if err != nil {
		return FileState{}, err
	}
	return FileState{Exists: true, Content: content, Mode: actual.Mode()}, nil
}
