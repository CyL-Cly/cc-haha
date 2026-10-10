package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

// SmokeSecurity isolates only the external OS credential primitive in the
// explicitly selected private Electron smoke profile. Agent, tools, model and
// credential-file behavior remain the normal implementation.
func SmokeSecurity(environment map[string]string) (func(context.Context, securestorage.Command) ([]byte, error), error) {
	requested := environment["CC_HAHA_ELECTRON_SMOKE_STATE_ROOT"]
	if requested == "" {
		return nil, nil
	}
	if !filepath.IsAbs(requested) || environment["CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG"] == "" {
		return nil, errors.New("desktop smoke requires explicit private root and window log")
	}
	root, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || filepath.Dir(root) == root || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("desktop smoke credential root must be a private directory")
	}
	for _, key := range []string{"HOME", "CLAUDE_CONFIG_DIR", "CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG"} {
		value := environment[key]
		if key == "HOME" && runtime.GOOS == "windows" {
			value = environment["USERPROFILE"]
		}
		if !filepath.IsAbs(value) {
			return nil, errors.New("desktop smoke credential path must be absolute: " + key)
		}
		ancestor := filepath.Clean(value)
		for {
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err == nil {
				relative, err := filepath.Rel(root, resolved)
				if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
					return nil, errors.New("desktop smoke credential path escapes private root: " + key)
				}
				break
			}
			if !os.IsNotExist(err) {
				return nil, err
			}
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				return nil, err
			}
			ancestor = parent
		}
	}
	return func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, nil
}
