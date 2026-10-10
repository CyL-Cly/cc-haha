package pluginregistry

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func PathWithinBase(base, relative string) (string, error) {
	base, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	path := relative
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)
	if path != base && !strings.HasPrefix(path, base+string(filepath.Separator)) {
		return "", fmt.Errorf("Path traversal detected: %q would escape the base directory", relative)
	}
	return path, nil
}

// CopyDirectory preserves Source copyDir's link rebasing, including raw broken
// links and absolute external targets. Directory modes use mkdir defaults;
// file modes follow copyFile. It does not follow symlinks during traversal.
func CopyDirectory(ctx context.Context, source, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0777); err != nil {
		return err
	}
	dir, err := os.Open(source)
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		from, to := filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())
		switch {
		case entry.IsDir():
			if err := CopyDirectory(ctx, from, to); err != nil {
				return err
			}
		case entry.Type().IsRegular():
			input, err := os.Open(from)
			if err != nil {
				return err
			}
			info, err := input.Stat()
			if err != nil {
				_ = input.Close()
				return err
			}
			output, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
			if err != nil {
				_ = input.Close()
				return err
			}
			_, copyErr := io.Copy(output, input)
			_ = input.Close()
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if err := os.Chmod(to, info.Mode().Perm()); err != nil {
				return err
			}
		case entry.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(from)
			if err != nil {
				return err
			}
			resolved, err := filepath.EvalSymlinks(from)
			if err == nil {
				resolved, err = filepath.Abs(resolved)
				if err != nil {
					return err
				}
				resolvedSource, resolveErr := filepath.EvalSymlinks(source)
				if resolveErr != nil {
					resolvedSource = source
				}
				resolvedSource, err = filepath.Abs(resolvedSource)
				if err != nil {
					return err
				}
				if resolved == resolvedSource || strings.HasPrefix(resolved, resolvedSource+string(filepath.Separator)) {
					relative, err := filepath.Rel(resolvedSource, resolved)
					if err != nil {
						return err
					}
					link, err = filepath.Rel(filepath.Dir(to), filepath.Join(target, relative))
					if err != nil {
						return err
					}
				} else {
					link = resolved
				}
			}
			if err := os.Symlink(link, to); err != nil {
				return err
			}
		}
	}
	return nil
}
