package clibootstrap

import (
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"golang.org/x/text/unicode/norm"
)

// ConfigHomeDirectory resolves getClaudeConfigHomeDir for native filesystem
// consumers. Callers that capture this value retain their existing lifetime;
// this does not rebuild a warmed store, watcher, cache or transcript writer.
// The launch environment itself remains raw and is passed unchanged to child
// transports. Finite external fixtures keep their already resolved root.
func (n *NativeBoundary) ConfigHomeDirectory() string {
	directory := n.nativeConfigHomePath()
	if !n.NativeProcess {
		return directory
	}
	if n.platform == "windows" {
		return nativepaths.ResolveWindows(directory, n.CWD)
	}
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(n.CWD, directory)
	}
	return directory
}

// Keep Source's requested path for skill metadata and watch-list projections;
// native filesystem adapters resolve it only at the actual OS boundary.
func (n *NativeBoundary) nativeConfigHomePath() string {
	if !n.NativeProcess {
		return n.Environment["CLAUDE_CONFIG_DIR"]
	}
	directory, present := n.nativeEnvironmentValue("CLAUDE_CONFIG_DIR")
	if !present {
		if n.platform == "windows" {
			directory = nativepaths.JoinWindows(n.Home, ".claude")
		} else {
			directory = filepath.Join(n.Home, ".claude")
		}
	}
	return norm.NFC.String(directory)
}

// Preserve the producer's displayed/requested string while using the native
// process's physical CWD for the actual OS call. Global configuration keeps
// its original relative path and first-lookup memo rather than rewriting it.
func (n *NativeBoundary) nativePhysicalPath(path string) string {
	if !n.NativeProcess || filepath.IsAbs(path) {
		return path
	}
	if n.platform == "windows" {
		return nativepaths.ResolveWindows(path, n.CWD)
	}
	return filepath.Join(n.CWD, path)
}
