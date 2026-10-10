package sessionrewind

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/checkpointfiles"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CapturedBackup contains the physical result only. The owning FileHistory
// producer allocates version/Date/state and records it through the existing
// pending transcript writer after this native operation settles.
type CapturedBackup = checkpointfiles.CapturedBackup

func sourceCaptureParent(base, path string) error {
	base, path = filepath.Clean(base), filepath.Clean(path)
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return err
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return nil
	}
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	for {
		realParent, err := filepath.EvalSymlinks(parent)
		if err == nil {
			rel, err = filepath.Rel(realBase, realParent)
			if err != nil {
				return err
			}
			if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
				return fmt.Errorf("FileHistory: Refusing path whose parent escapes the project through a symbolic link: %s", path)
			}
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

func (n NativeFiles) ensureCaptureDirectories(id string) ([]directoryIdentity, error) {
	if !filepath.IsAbs(n.ConfigHome) || !safeSegment(id) {
		return nil, ErrUnsafeCheckpointFile
	}
	if err := os.MkdirAll(n.ConfigHome, 0777); err != nil {
		return nil, err
	}
	entries := []directoryIdentity{}
	path := n.ConfigHome
	for _, segment := range []string{"file-history", id} {
		path = filepath.Join(path, segment)
		if err := os.Mkdir(path, 0777); err != nil && !os.IsExist(err) {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("FileHistory: Refusing unsafe directory: %s", path)
		}
		entries = append(entries, directoryIdentity{path, info})
	}
	return entries, nil
}

// CaptureBackup follows original createBackup: no-follow/identity checked
// origin, safe destination directories, exclusive temporary copy, fsync and
// atomic rename. It shares the restore reader's linked-file boundary.
func (n NativeFiles) CaptureBackup(ctx context.Context, id, base, path string, version int, suffix string, uuid func() (string, error)) (result CapturedBackup, failure error) {
	if !filepath.IsAbs(base) || !filepath.IsAbs(path) || uuid == nil || version < 1 {
		return result, ErrUnsafeCheckpointFile
	}
	initial, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	links, err := pathLinkCount(path, initial)
	if err != nil {
		return result, err
	}
	if !safeFile(initial, links) {
		return result, fmt.Errorf("FileHistory: Refusing to snapshot unsafe linked file: %s", path)
	}
	source, err := openNoFollow(path, os.O_RDONLY, 0)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer source.Close()
	if err = sourceCaptureParent(base, path); err != nil {
		return result, err
	}
	opened, err := source.Stat()
	if err != nil {
		return result, err
	}
	links, err = fileLinkCount(source, opened)
	if err != nil {
		return result, err
	}
	if !safeFile(opened, links) {
		return result, fmt.Errorf("FileHistory: Refusing to snapshot unsafe linked file: %s", path)
	}
	if !os.SameFile(initial, opened) {
		return result, fmt.Errorf("FileHistory: Refusing to snapshot a file that changed while opening: %s", path)
	}
	dirs, err := n.ensureCaptureDirectories(id)
	if err != nil {
		return result, err
	}
	hash := sha256.Sum256([]byte(path))
	name := fmt.Sprintf("%s@v%d", hex.EncodeToString(hash[:8]), version)
	if suffix != "" {
		name += "-" + suffix
	}
	if !safeSegment(name) {
		return result, ErrUnsafeCheckpointFile
	}
	unique, err := uuid()
	if err != nil {
		return result, err
	}
	if !safeSegment(unique) {
		return result, ErrUnsafeCheckpointFile
	}
	target := filepath.Join(n.ConfigHome, "file-history", id, name)
	temporary := target + "." + unique + ".tmp"
	destination, err := openNoFollow(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, opened.Mode().Perm())
	if err != nil {
		return result, err
	}
	committed := false
	defer func() {
		_ = destination.Close()
		if !committed && unchangedDirectories(dirs) == nil {
			_ = os.Remove(temporary)
		}
	}()
	buffer := make([]byte, 1024*1024)
	var position int64
	for {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		count, e := source.ReadAt(buffer, position)
		if count > 0 {
			written := 0
			for written < count {
				n, e := destination.WriteAt(buffer[written:count], position+int64(written))
				if e != nil {
					return result, e
				}
				if n == 0 {
					return result, io.ErrShortWrite
				}
				written += n
			}
			position += int64(count)
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return result, e
		}
	}
	if err = destination.Chmod(opened.Mode().Perm()); err != nil {
		return result, err
	}
	if err = destination.Sync(); err != nil {
		return result, err
	}
	if err = destination.Close(); err != nil {
		return result, err
	}
	if err = unchangedDirectories(dirs); err != nil {
		return result, err
	}
	copyInfo, err := os.Lstat(temporary)
	if err != nil {
		return result, err
	}
	links, err = pathLinkCount(temporary, copyInfo)
	if err != nil {
		return result, err
	}
	if !safeFile(copyInfo, links) {
		return result, ErrUnsafeCheckpointFile
	}
	if err = os.Rename(temporary, target); err != nil {
		return result, err
	}
	committed = true
	result.Name = &name
	result.Size = opened.Size()
	return result, nil
}
