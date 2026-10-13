package server

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/storagerelocations"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type filesystemEntry struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	IsDirectory  bool   `json:"isDirectory"`
	RelativePath string `json:"relativePath"`
}

func normalizeDriveRoot(value string) string {
	if runtime.GOOS == "windows" && len(value) == 2 && value[1] == ':' && (value[0] >= 'a' && value[0] <= 'z' || value[0] >= 'A' && value[0] <= 'Z') {
		return value + `\`
	}
	return value
}

func (server *Server) filesystemPath(value string) string {
	if runtime.GOOS == "windows" && len(value) > 0 && (value[0] == '/' || value[0] == '\\') && filepath.VolumeName(value) == "" {
		value = filepath.VolumeName(server.options.WorkDir) + value
	}
	return resolveFrom(server.options.WorkDir, normalizeDriveRoot(value))
}

func (server *Server) relocatedFilesystemPath(value string) (string, error) {
	return storagerelocations.Resolve(server.filesystemPath(value), server.options.ConfigDir)
}

// Missing descendants inherit the closest existing ancestor's canonical path,
// including the macOS /var and /tmp aliases used by native directory pickers.
func canonicalAccessPath(value string) string {
	resolved := filepath.Clean(value)
	candidate := resolved
	suffix := []string{}
	for {
		if real, err := filepath.EvalSymlinks(candidate); err == nil {
			return filepath.Join(append([]string{real}, suffix...)...)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return resolved
		}
		suffix = append([]string{filepath.Base(candidate)}, suffix...)
		candidate = parent
	}
}

func sameOrInsideFilesystemPath(path, root string) bool {
	if runtime.GOOS == "windows" {
		path, root = strings.ToLower(path), strings.ToLower(root)
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || relative != "" && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}

func (server *Server) RegisterFilesystemAccessRoot(value string) {
	if value == "" {
		return
	}
	path := canonicalAccessPath(server.filesystemPath(value))
	server.mu.Lock()
	if server.filesystemRoots == nil {
		server.filesystemRoots = map[string]bool{}
	}
	server.filesystemRoots[path] = true
	server.mu.Unlock()
}

// Callers use this only after an actual, permission-authorized file change.
func (server *Server) RegisterChangedFileAccessRoot(file, workDir string) {
	if file == "" {
		return
	}
	path := canonicalAccessPath(server.filesystemPath(file))
	if workDir != "" && sameOrInsideFilesystemPath(path, canonicalAccessPath(server.filesystemPath(workDir))) {
		return
	}
	server.RegisterFilesystemAccessRoot(filepath.Dir(path))
}

func (server *Server) filesystemAllowed(path string) bool {
	path = canonicalAccessPath(path)
	for _, root := range []string{server.options.HomeDir, "/tmp", server.options.ConfigDir} {
		if sameOrInsideFilesystemPath(path, canonicalAccessPath(server.filesystemPath(root))) {
			return true
		}
	}
	if runtime.GOOS == "darwin" && sameOrInsideFilesystemPath(path, canonicalAccessPath("/private/tmp")) {
		return true
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	for root := range server.filesystemRoots {
		if sameOrInsideFilesystemPath(path, root) {
			return true
		}
	}
	return false
}

func (server *Server) filesystemHTTP(writer http.ResponseWriter, request *http.Request, parts []string) error {
	if len(parts) != 3 || parts[2] != "browse" && parts[2] != "file" {
		writeJSON(writer, 404, map[string]string{"error": "Not found"})
		return nil
	}
	if parts[2] == "file" {
		return server.filesystemImage(writer, request)
	}
	value := request.URL.Query().Get("path")
	if value == "" {
		value = server.options.HomeDir
	}
	resolved, err := server.relocatedFilesystemPath(value)
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		if !server.filesystemAllowed(resolved) {
			writeJSON(writer, 403, map[string]string{"error": "Access denied: path outside allowed directory"})
		} else {
			writeJSON(writer, 404, map[string]string{"error": "Cannot read directory: path not found", "path": resolved})
		}
		return nil
	}
	if !server.filesystemAllowed(canonical) {
		writeJSON(writer, 403, map[string]string{"error": "Access denied: path outside allowed directory"})
		return nil
	}
	stat, err := os.Stat(canonical)
	if err != nil {
		writeJSON(writer, 500, map[string]string{"error": "Cannot read directory: " + err.Error(), "path": canonical})
		return nil
	}
	if !stat.IsDir() {
		writeJSON(writer, 400, map[string]string{"error": "Not a directory", "path": canonical})
		return nil
	}
	if request.URL.Query().Get("search") != "" {
		return server.filesystemSearchHTTP(writer, request, canonical)
	}
	entries, err := os.ReadDir(canonical)
	if err != nil {
		writeJSON(writer, 500, map[string]string{"error": "Cannot read directory: " + err.Error(), "path": canonical})
		return nil
	}
	results := []filesystemEntry{}
	includeFiles := request.URL.Query().Get("includeFiles") == "true"
	for _, entry := range entries {
		if entry.IsDir() {
			switch strings.ToLower(entry.Name()) {
			case ".git", ".svn", ".hg", ".bzr", ".jj", ".sl":
				continue
			}
		} else if !includeFiles {
			continue
		}
		results = append(results, filesystemEntry{Name: entry.Name(), Path: filepath.Join(canonical, entry.Name()), IsDirectory: entry.IsDir(), RelativePath: entry.Name()})
	}
	// The shared migration's declared host-locale profile is English Unicode;
	// full ICU/version collation remains an explicit parity boundary.
	collator := collate.New(language.English)
	sort.SliceStable(results, func(a, b int) bool {
		if results[a].IsDirectory != results[b].IsDirectory {
			return results[a].IsDirectory
		}
		return collator.CompareString(results[a].Name, results[b].Name) < 0
	})
	writeJSON(writer, 200, map[string]any{"currentPath": canonical, "parentPath": filepath.Dir(canonical), "entries": results})
	return nil
}

func (server *Server) filesystemImage(writer http.ResponseWriter, request *http.Request) error {
	value := request.URL.Query().Get("path")
	if value == "" {
		writeJSON(writer, 400, map[string]string{"error": "Missing path parameter"})
		return nil
	}
	if value == "~" {
		value = server.options.HomeDir
	} else if strings.HasPrefix(value, "~/") || runtime.GOOS == "windows" && strings.HasPrefix(value, `~\`) {
		value = filepath.Join(server.options.HomeDir, value[2:])
	}
	resolved, err := server.relocatedFilesystemPath(value)
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		if !server.filesystemAllowed(resolved) {
			writeJSON(writer, 403, map[string]string{"error": "Access denied: path outside allowed directory"})
		} else {
			writeJSON(writer, 404, map[string]string{"error": "File not found"})
		}
		return nil
	}
	if !server.filesystemAllowed(canonical) {
		writeJSON(writer, 403, map[string]string{"error": "Access denied: path outside allowed directory"})
		return nil
	}
	mime := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".svg": "image/svg+xml", ".bmp": "image/bmp", ".ico": "image/x-icon", ".avif": "image/avif"}[strings.ToLower(filepath.Ext(canonical))]
	if mime == "" {
		writeJSON(writer, 400, map[string]string{"error": "Unsupported file type"})
		return nil
	}
	stat, err := os.Stat(canonical)
	if err != nil {
		writeJSON(writer, 404, map[string]string{"error": "File not found"})
		return nil
	}
	if !stat.Mode().IsRegular() {
		writeJSON(writer, 400, map[string]string{"error": "Not a file"})
		return nil
	}
	if stat.Size() > 50*1024*1024 {
		writeJSON(writer, 400, map[string]string{"error": "File too large"})
		return nil
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		writeJSON(writer, 404, map[string]string{"error": "File not found"})
		return nil
	}
	writer.Header().Set("Content-Type", mime)
	writer.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	writer.Header().Set("Cache-Control", "private, no-cache")
	writer.WriteHeader(200)
	_, _ = writer.Write(data)
	return nil
}
