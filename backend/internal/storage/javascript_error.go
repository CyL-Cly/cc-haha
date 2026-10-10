package storage

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

type JavaScriptErrorContext struct {
	// MkdirAll can return an ancestor's path; Bun reports the requested path.
	RequestedPath string
	// Atomic replacement has just written its temporary regular file. Only
	// that proven source kind allows correction of Go rename's EEXIST precheck.
	RenameSourceIsFile bool
}

// JavaScriptErrorText is used only where the source business service inserts
// `${error}` into an ApiError message. It never replaces the native error,
// changes errors.Is/Unwrap, reads a path, or decides an HTTP status.
// These errno descriptions and operation/path forms are backed by actual Bun
// private-filesystem fixtures. Other native combinations retain their text;
// native Windows/Linux execution remains a separate verification obligation.
func JavaScriptErrorText(err error, contexts ...JavaScriptErrorContext) string {
	if err == nil {
		return "null"
	}
	name := "Error"
	if named, valid := err.(interface{ ErrorName() string }); valid {
		name = named.ErrorName()
	}
	message := err.Error()
	var context JavaScriptErrorContext
	if len(contexts) > 0 {
		context = contexts[0]
	}
	switch native := err.(type) {
	case *os.PathError:
		if code, description, known := sourceErrno(native.Err); known {
			path := native.Path
			if native.Op == "mkdir" && context.RequestedPath != "" {
				path = context.RequestedPath
				if errors.Is(native.Err, syscall.ENOTDIR) && filepath.Clean(native.Path) == filepath.Clean(path) {
					code, description = "EEXIST", "file already exists"
				}
			}
			switch native.Op {
			case "open", "mkdir", "unlink":
				message = code + ": " + description + ", " + native.Op + " '" + path + "'"
			case "read":
				// Bun readFile's failed read uses the fd operation, without path.
				message = code + ": " + description + ", read"
			}
		}
	case *os.LinkError:
		if code, description, known := sourceErrno(native.Err); known && native.Op == "rename" {
			if context.RenameSourceIsFile && errors.Is(native.Err, syscall.EEXIST) {
				code, description = "EISDIR", "illegal operation on a directory"
			}
			message = code + ": " + description + ", rename '" + native.Old + "' -> '" + native.New + "'"
		}
	}
	if name == "" {
		return message
	}
	return name + ": " + message
}

func sourceErrno(err error) (code, description string, known bool) {
	switch {
	case errors.Is(err, syscall.EISDIR):
		return "EISDIR", "illegal operation on a directory", true
	case errors.Is(err, syscall.ENOTDIR):
		return "ENOTDIR", "not a directory", true
	case err == fs.ErrPermission || errors.Is(err, syscall.EACCES):
		return "EACCES", "permission denied", true
	case err == fs.ErrExist || errors.Is(err, syscall.EEXIST):
		return "EEXIST", "file already exists", true
	case err == fs.ErrNotExist || errors.Is(err, syscall.ENOENT):
		return "ENOENT", "no such file or directory", true
	default:
		return "", "", false
	}
}
