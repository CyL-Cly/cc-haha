package clibootstrap

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// Original prepareInputImages admits all root realpaths, then all image
// realpaths, before awaiting each Promise.all. One early failure rejects that
// aggregate while the admitted physical file operations retain their lifetime.
func nativeImageAll[T any](m *MainPrint, scope *cooperative.Scope, children []*ownerasync.OwnerFuture[T]) (*ownerasync.OwnerFuture[[]T], error) {
	result, err := ownerasync.NewOwnerFuture[[]T](m.Owner)
	if err != nil {
		return nil, err
	}
	values := make([]T, len(children))
	remaining := len(children)
	if remaining == 0 {
		return result, result.Resolve(scope, values)
	}
	for index, child := range children {
		_, err := child.Then(scope, func(next *cooperative.Scope, value T, failure error) error {
			if failure != nil {
				return result.Reject(next, failure)
			}
			values[index] = value
			remaining--
			if remaining == 0 {
				return result.Resolve(next, values)
			}
			return nil
		})
		if err != nil {
			_ = result.RejectAdministrative(err)
			return nil, err
		}
	}
	return result, nil
}

func (m *MainPrint) prepareNativeImageInputs(ctx context.Context, scope *cooperative.Scope, paths, roots []string, files imagegeneration.Files) ([]imagegeneration.PreparedImage, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if len(paths) == 0 {
		return []imagegeneration.PreparedImage{}, nil
	}
	if len(paths) > 3 {
		return nil, errors.New("Image editing supports at most 3 input images per call.")
	}
	physicalContext := context.WithoutCancel(ctx)
	rootPromises := make([]*ownerasync.OwnerFuture[string], 0, len(roots))
	for _, root := range roots {
		future, err := nativeSkillWork(m, scope, func() (string, error) {
			resolved, err := files.Realpath(physicalContext, root)
			if err != nil {
				return filepath.Abs(root)
			}
			return resolved, nil
		})
		if err != nil {
			return nil, err
		}
		rootPromises = append(rootPromises, future)
	}
	rootAll, err := nativeImageAll(m, scope, rootPromises)
	if err != nil {
		return nil, err
	}
	resolvedRoots, err := rootAll.Await(scope)
	if err != nil {
		return nil, err
	}
	images := make([]*ownerasync.OwnerFuture[imagegeneration.PreparedImage], 0, len(paths))
	for _, path := range paths {
		future, err := m.startNativeImageInput(ctx, scope, path, resolvedRoots, files)
		if err != nil {
			return nil, err
		}
		images = append(images, future)
	}
	all, err := nativeImageAll(m, scope, images)
	if err != nil {
		return nil, err
	}
	return all.Await(scope)
}

func (m *MainPrint) startNativeImageInput(ctx context.Context, scope *cooperative.Scope, path string, roots []string, files imagegeneration.Files) (*ownerasync.OwnerFuture[imagegeneration.PreparedImage], error) {
	physicalContext := context.WithoutCancel(ctx)
	result, err := ownerasync.NewOwnerFuture[imagegeneration.PreparedImage](m.Owner)
	if err != nil {
		return nil, err
	}
	resolved, err := nativeSkillWork(m, scope, func() (string, error) { return files.Realpath(physicalContext, path) })
	if err != nil {
		return nil, err
	}
	_, err = resolved.Then(scope, func(next *cooperative.Scope, resolved string, failure error) error {
		allowed := false
		if failure == nil {
			for _, root := range roots {
				rel, err := filepath.Rel(root, resolved)
				allowed = allowed || err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
			}
			if !allowed {
				var err error
				allowed, err = m.nativeUserImageAuthorized(next, resolved)
				if err != nil {
					return result.Reject(next, err)
				}
			}
		}
		if !allowed {
			return result.Reject(next, fmt.Errorf("Image edit input was not provided by the user in this session: %s. Usable inputs are images the user pasted or dropped into the chat, attached with @, or images returned by an earlier ImageGen call. Ask the user to attach the image instead of reading it from disk.", path))
		}
		stat, err := nativeSkillWork(m, next, func() (fs.FileInfo, error) { return files.Stat(physicalContext, resolved) })
		if err != nil {
			return result.Reject(next, err)
		}
		_, err = stat.Then(next, func(next *cooperative.Scope, info fs.FileInfo, failure error) error {
			if failure != nil {
				return result.Reject(next, failure)
			}
			if !info.Mode().IsRegular() {
				return result.Reject(next, fmt.Errorf("Image edit input is not a regular file: %s", path))
			}
			if info.Size() <= 0 || info.Size() > imagegeneration.MaxInputBytes {
				return result.Reject(next, errors.New("Image edit inputs must be non-empty and no larger than 20 MB each."))
			}
			read, err := nativeSkillWork(m, next, func() ([]byte, error) { return files.ReadNoFollow(physicalContext, resolved) })
			if err != nil {
				return result.Reject(next, err)
			}
			_, err = read.Then(next, func(next *cooperative.Scope, data []byte, failure error) error {
				if failure != nil {
					return result.Reject(next, failure)
				}
				mime, _ := imagegeneration.DetectImage(data)
				if mime == "" {
					return result.Reject(next, errors.New("Image edit inputs must be PNG, JPEG, or WebP files."))
				}
				return result.Resolve(next, imagegeneration.PreparedImage{Bytes: data, MIMEType: mime, FileName: filepath.Base(resolved), DataURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)})
			})
			if err != nil {
				return result.Reject(next, err)
			}
			return nil
		})
		if err != nil {
			return result.Reject(next, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
