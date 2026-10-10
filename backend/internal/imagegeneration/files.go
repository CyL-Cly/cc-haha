package imagegeneration

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

const MaxGeneratedBytes = 30 * 1024 * 1024
const MaxInputBytes = 20 * 1024 * 1024

type Files interface {
	Realpath(context.Context, string) (string, error)
	Stat(context.Context, string) (fs.FileInfo, error)
	ReadNoFollow(context.Context, string) ([]byte, error)
	MkdirAll(context.Context, string, fs.FileMode) error
	Chmod(context.Context, string, fs.FileMode) error
	WriteExclusive(context.Context, string, []byte, fs.FileMode) error
}

func DetectImage(data []byte) (mime, extension string) {
	if len(data) >= 8 && string(data[:8]) == string([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}) {
		return "image/png", "png"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg", "jpg"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp", "webp"
	}
	return "", ""
}
func pathInside(root, candidate string) bool {
	path, err := filepath.Rel(root, candidate)
	return err == nil && (path == "." || path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator)) && !filepath.IsAbs(path))
}
func PrepareInputImages(ctx context.Context, files Files, paths, roots []string, userProvided func(string) bool) ([]PreparedImage, error) {
	var authorize func(context.Context, string) (bool, error)
	if userProvided != nil {
		authorize = func(_ context.Context, path string) (bool, error) { return userProvided(path), nil }
	}
	return PrepareInputImagesWithAuthorization(ctx, files, paths, roots, authorize)
}

// Authorization may cross to the root Harness owner; its administrative
// failures must retain their identity rather than becoming an upload denial.
func PrepareInputImagesWithAuthorization(ctx context.Context, files Files, paths, roots []string, userProvided func(context.Context, string) (bool, error)) ([]PreparedImage, error) {
	if len(paths) == 0 {
		return []PreparedImage{}, nil
	}
	if len(paths) > 3 {
		return nil, errors.New("Image editing supports at most 3 input images per call.")
	}
	resolvedRoots := make([]string, len(roots))
	for i, root := range roots {
		resolved, err := files.Realpath(ctx, root)
		if err != nil {
			resolved, err = filepath.Abs(root)
			if err != nil {
				return nil, err
			}
		}
		resolvedRoots[i] = resolved
	}
	images := make([]PreparedImage, len(paths))
	for i, path := range paths {
		resolved, err := files.Realpath(ctx, path)
		allowed := false
		if err == nil {
			for _, root := range resolvedRoots {
				allowed = allowed || pathInside(root, resolved)
			}
			if !allowed && userProvided != nil {
				allowed, err = userProvided(ctx, resolved)
				if err != nil {
					return nil, err
				}
			}
		}
		if !allowed {
			return nil, fmt.Errorf("Image edit input was not provided by the user in this session: %s. Usable inputs are images the user pasted or dropped into the chat, attached with @, or images returned by an earlier ImageGen call. Ask the user to attach the image instead of reading it from disk.", path)
		}
		info, err := files.Stat(ctx, resolved)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Image edit input is not a regular file: %s", path)
		}
		if info.Size() <= 0 || info.Size() > MaxInputBytes {
			return nil, errors.New("Image edit inputs must be non-empty and no larger than 20 MB each.")
		}
		data, err := files.ReadNoFollow(ctx, resolved)
		if err != nil {
			return nil, err
		}
		mime, _ := DetectImage(data)
		if mime == "" {
			return nil, errors.New("Image edit inputs must be PNG, JPEG, or WebP files.")
		}
		images[i] = PreparedImage{DataURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), FileName: filepath.Base(resolved), MIMEType: mime, Bytes: data}
	}
	return images, nil
}
func PersistImage(ctx context.Context, files Files, dir string, data []byte, index int, revised string, now int64, uuid string) (GeneratedImage, error) {
	mime, extension := DetectImage(data)
	if mime == "" {
		return GeneratedImage{}, errors.New("The provider response was not a supported PNG, JPEG, or WebP image.")
	}
	path := filepath.Join(dir, fmt.Sprintf("%d-%d-%s.%s", now, index+1, uuid, extension))
	if err := files.WriteExclusive(ctx, path, data, 0600); err != nil {
		return GeneratedImage{}, err
	}
	return GeneratedImage{Path: path, MIMEType: mime, RevisedPrompt: revised}, nil
}
