package server

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

type workspacePath struct{ absolute, canonical, relative, root string }
type workspaceDocument struct {
	preview, mime string
	limit         int64
}
type workspaceTreeEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDirectory bool   `json:"isDirectory"`
}

var workspaceLanguages = map[string]string{"cjs": "javascript", "css": "css", "go": "go", "html": "html", "java": "java", "js": "javascript", "json": "json", "jsx": "jsx", "md": "markdown", "mjs": "javascript", "py": "python", "rs": "rust", "sh": "bash", "sql": "sql", "ts": "typescript", "tsx": "tsx", "txt": "text", "xml": "xml", "yaml": "yaml", "yml": "yaml"}
var workspaceImages = map[string]string{"apng": "image/apng", "avif": "image/avif", "gif": "image/gif", "jpeg": "image/jpeg", "jpg": "image/jpeg", "png": "image/png", "svg": "image/svg+xml", "webp": "image/webp"}
var workspaceDocuments = map[string]workspaceDocument{
	"pdf":  {"pdf", "application/pdf", 100 * 1024 * 1024},
	"docx": {"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", 30 * 1024 * 1024},
	"xlsx": {"xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", 30 * 1024 * 1024},
	"xlsm": {"xlsx", "application/vnd.ms-excel.sheet.macroEnabled.12", 30 * 1024 * 1024},
	"xls":  {"xlsx", "application/vnd.ms-excel", 30 * 1024 * 1024},
	"csv":  {"xlsx", "text/csv", 10 * 1024 * 1024},
	"tsv":  {"xlsx", "text/tab-separated-values", 10 * 1024 * 1024},
}

func workspaceExtension(path string) string {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") && !strings.Contains(base[1:], ".") {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
}
func workspaceLanguage(path string) string {
	if language := workspaceLanguages[workspaceExtension(path)]; language != "" {
		return language
	}
	return "text"
}
func workspaceMissing(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

func sourceMtimeMillis(stat os.FileInfo) float64 {
	// Native JS stat forms milliseconds from sec/nsec independently. Converting
	// the whole nanosecond epoch to float first loses the fractional version.
	return float64(stat.ModTime().Unix())*1000 + float64(stat.ModTime().Nanosecond())/1e6
}
func normalizeWorkspacePath(path string) string {
	if path == "." {
		return ""
	}
	return filepath.ToSlash(path)
}
func workspaceFSError(prefix, path string, err error) string {
	return fmt.Sprintf("%s (%s): %s", prefix, path, err)
}
func (server *Server) registeredWorkspacePath(path string) bool {
	path = canonicalAccessPath(path)
	server.mu.Lock()
	defer server.mu.Unlock()
	for root := range server.filesystemRoots {
		if sameOrInsideFilesystemPath(path, root) {
			return true
		}
	}
	return false
}
func (server *Server) resolveWorkspacePath(info sessionInfo, requested string) (workspacePath, error) {
	workDir := server.filesystemPath(info.WorkDir)
	canonicalRoot, err := filepath.EvalSymlinks(workDir)
	if workspaceMissing(err) {
		return workspacePath{}, fmt.Errorf("Workspace root is missing: %s", workDir)
	}
	if err != nil {
		return workspacePath{}, fmt.Errorf("%s", workspaceFSError("Failed to canonicalize workspace root", workDir, err))
	}
	absolute := requested
	if !filepath.IsAbs(normalizeDriveRoot(requested)) {
		absolute = filepath.Join(workDir, requested)
	}
	absolute = unicodeWorkspacePath(filepath.Clean(normalizeDriveRoot(absolute)))
	if !sameOrInsideFilesystemPath(absolute, workDir) {
		if filepath.IsAbs(normalizeDriveRoot(requested)) && server.registeredWorkspacePath(absolute) {
			canonical := absolute
			if real, err := filepath.EvalSymlinks(absolute); err == nil {
				canonical = real
			}
			return workspacePath{absolute, canonical, normalizeWorkspacePath(requested), filepath.Dir(absolute)}, nil
		}
		return workspacePath{}, &apiError{403, "FORBIDDEN", "Path is outside workspace: " + requested}
	}
	probe := absolute
	suffix := []string{}
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			canonical := filepath.Join(append([]string{real}, suffix...)...)
			if !sameOrInsideFilesystemPath(canonical, canonicalRoot) {
				return workspacePath{}, &apiError{403, "FORBIDDEN", "Path is outside workspace: " + requested}
			}
			relative, err := filepath.Rel(workDir, absolute)
			if err != nil {
				return workspacePath{}, err
			}
			return workspacePath{absolute, canonical, normalizeWorkspacePath(relative), workDir}, nil
		}
		if !workspaceMissing(err) {
			return workspacePath{}, fmt.Errorf("%s", workspaceFSError("Failed to canonicalize workspace path", absolute, err))
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return workspacePath{}, err
		}
		suffix = append([]string{filepath.Base(probe)}, suffix...)
		probe = parent
	}
}
func workspaceTree(resolved workspacePath) map[string]any {
	result := map[string]any{"path": resolved.relative, "entries": []workspaceTreeEntry{}}
	stat, err := os.Stat(resolved.absolute)
	if workspaceMissing(err) || err == nil && !stat.IsDir() {
		result["state"] = "missing"
		return result
	}
	if err != nil {
		result["state"] = "error"
		result["error"] = workspaceFSError("Failed to stat workspace path", resolved.absolute, err)
		return result
	}
	entries, err := os.ReadDir(resolved.absolute)
	if err != nil {
		result["state"] = "error"
		result["error"] = workspaceFSError("Failed to read workspace directory", resolved.absolute, err)
		return result
	}
	visible := []workspaceTreeEntry{}
	for _, entry := range entries {
		if entry.IsDir() && vcsDirectory(entry.Name()) {
			continue
		}
		relative, err := filepath.Rel(resolved.root, filepath.Join(resolved.absolute, entry.Name()))
		if err != nil {
			continue
		}
		visible = append(visible, workspaceTreeEntry{entry.Name(), normalizeWorkspacePath(relative), entry.IsDir()})
	}
	comparator := collate.New(language.English)
	sort.SliceStable(visible, func(i, j int) bool {
		if visible[i].IsDirectory != visible[j].IsDirectory {
			return visible[i].IsDirectory
		}
		return comparator.CompareString(visible[i].Name, visible[j].Name) < 0
	})
	result["state"], result["entries"] = "ok", visible
	return result
}
func workspaceReadFile(resolved workspacePath) map[string]any {
	result := map[string]any{"path": resolved.relative, "language": workspaceLanguage(resolved.absolute), "size": int64(0)}
	stat, err := os.Stat(resolved.absolute)
	if workspaceMissing(err) || err == nil && !stat.Mode().IsRegular() {
		result["state"] = "missing"
		return result
	}
	if err != nil {
		result["state"] = "error"
		result["error"] = workspaceFSError("Failed to stat workspace path", resolved.absolute, err)
		return result
	}
	result["size"] = stat.Size()
	extension := workspaceExtension(resolved.absolute)
	if document, exists := workspaceDocuments[extension]; exists {
		result["state"] = "ok"
		if stat.Size() > document.limit {
			result["state"] = "too_large"
		}
		result["previewType"], result["language"], result["mimeType"] = document.preview, document.preview, document.mime
		result["version"] = strconv.FormatFloat(sourceMtimeMillis(stat), 'f', -1, 64) + "-" + strconv.FormatInt(stat.Size(), 10)
		return result
	}
	image := workspaceImages[extension]
	var content []byte
	if image == "" && stat.Size() > 1024*1024 {
		file, openErr := os.Open(resolved.absolute)
		err = openErr
		if err == nil {
			content, err = io.ReadAll(io.LimitReader(file, 1024*1024))
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
	} else {
		content, err = os.ReadFile(resolved.absolute)
	}
	if err != nil {
		result["state"] = "error"
		result["error"] = workspaceFSError("Failed to read workspace file", resolved.absolute, err)
		return result
	}
	if image != "" {
		result["state"], result["previewType"], result["language"], result["mimeType"] = "ok", "image", "image", image
		result["dataUrl"] = "data:" + image + ";base64," + base64.StdEncoding.EncodeToString(content)
		return result
	}
	if bytes.IndexByte(content, 0) >= 0 {
		result["state"], result["language"] = "binary", "binary"
		return result
	}
	result["state"], result["previewType"], result["content"] = "ok", "text", string(protocol.DecodeJavaScriptUTF8(content))
	result["truncated"], result["readBytes"] = int64(len(content)) < stat.Size(), len(content)
	return result
}
func workspaceRaw(writer http.ResponseWriter, resolved workspacePath) error {
	document, exists := workspaceDocuments[workspaceExtension(resolved.absolute)]
	if !exists {
		return &apiError{415, "UNSUPPORTED_MEDIA_TYPE", "Unsupported preview type: " + resolved.relative}
	}
	file, err := os.Open(resolved.canonical)
	if workspaceMissing(err) {
		return notFound("File not found: " + resolved.relative)
	}
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() {
		return notFound("File not found: " + resolved.relative)
	}
	if stat.Size() > document.limit {
		return &apiError{413, "PAYLOAD_TOO_LARGE", fmt.Sprintf("File exceeds the %d byte preview limit: %s", document.limit, resolved.relative)}
	}
	writer.Header().Set("Content-Type", document.mime)
	writer.Header().Set("Cache-Control", "private, no-cache")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	writer.WriteHeader(200)
	_, _ = io.Copy(writer, file)
	return nil
}
func (server *Server) workspaceHTTP(writer http.ResponseWriter, request *http.Request, info sessionInfo, parts []string) error {
	if request.Method != http.MethodGet {
		return methodNotAllowed(request.Method)
	}
	if len(parts) < 5 {
		return notFound("Unknown workspace resource: workspace")
	}
	resource := parts[4]
	if resource == "stat" {
		paths := []string{}
		seen := map[string]bool{}
		for _, path := range request.URL.Query()["path"] {
			if path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
		if len(paths) == 0 {
			return badRequest("path query parameter is required for workspace stat")
		}
		if len(paths) > 20 {
			return badRequest("workspace stat accepts at most 20 paths")
		}
		files := []map[string]any{}
		for _, path := range paths {
			item := map[string]any{"path": path, "state": "unavailable"}
			resolved, e := server.resolveWorkspacePath(info, path)
			if e == nil {
				stat, e := os.Stat(resolved.absolute)
				if workspaceMissing(e) || e == nil && !stat.Mode().IsRegular() {
					item["state"] = "missing"
				} else if e == nil {
					item["state"], item["mtimeMs"] = "file", sourceMtimeMillis(stat)
				}
			}
			files = append(files, item)
		}
		writeJSON(writer, 200, map[string]any{"files": files})
		return nil
	}
	if resource == "search" {
		query := trimJS(request.URL.Query().Get("query"))
		if query == "" {
			return badRequest("query parameter is required for workspace search")
		}
		entries, err := server.filesystemSearchEntries(request.Context(), server.filesystemPath(info.WorkDir), query, true, false, func(length int) int { return min(length, 201) })
		if err != nil {
			return err
		}
		visible := []workspaceTreeEntry{}
		for _, entry := range entries[:min(len(entries), 200)] {
			visible = append(visible, workspaceTreeEntry{entry.Name, strings.ReplaceAll(entry.RelativePath, `\`, "/"), entry.IsDirectory})
		}
		writeJSON(writer, 200, map[string]any{"state": "ok", "query": query, "truncated": len(entries) > 200, "entries": visible})
		return nil
	}
	if resource == "status" {
		result, err := server.workspaceStatus(request.Context(), info)
		if err != nil {
			return err
		}
		writeJSON(writer, 200, result)
		return nil
	}
	if resource == "diff" {
		path := request.URL.Query().Get("path")
		if path == "" {
			return badRequest("path query parameter is required for workspace diff")
		}
		result, err := server.workspaceDiff(request.Context(), info, path)
		if err != nil {
			return err
		}
		writeJSON(writer, 200, result)
		return nil
	}
	if resource == "watch" {
		return server.workspaceWatchHTTP(writer, request, info)
	}
	if resource != "tree" && resource != "file" && resource != "raw" {
		return notFound("Unknown workspace resource: " + resource)
	}
	path := request.URL.Query().Get("path")
	if resource != "tree" && path == "" {
		return badRequest("path query parameter is required for workspace " + resource)
	}
	resolved, err := server.resolveWorkspacePath(info, path)
	if err != nil {
		return err
	}
	if resource == "raw" {
		return workspaceRaw(writer, resolved)
	}
	var result map[string]any
	if resource == "file" {
		result = workspaceReadFile(resolved)
	} else {
		result = workspaceTree(resolved)
	}
	writeJSON(writer, 200, result)
	return nil
}

// Resolve Unicode normalization aliases segment-by-segment while retaining the
// existing canonical containment checks. Exact filenames always win.
func unicodeWorkspacePath(path string) string {
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)
	current := volume + string(filepath.Separator)
	for _, segment := range strings.Split(strings.TrimLeft(rest, string(filepath.Separator)), string(filepath.Separator)) {
		if segment == "" {
			continue
		}
		exact := filepath.Join(current, segment)
		if _, err := os.Stat(exact); err == nil {
			current = exact
			continue
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return path
		}
		match := ""
		for _, entry := range entries {
			if norm.NFC.String(entry.Name()) == norm.NFC.String(segment) {
				match = entry.Name()
				break
			}
		}
		if match == "" {
			return path
		}
		current = filepath.Join(current, match)
	}
	return current
}
