package server

import (
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const maxPreviewFileBytes = 2 * 1024 * 1024 * 1024
const maxPreviewHTMLBytes = 10 * 1024 * 1024
const previewHTMLCSP = "sandbox allow-scripts allow-same-origin allow-modals allow-downloads allow-popups; default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; media-src 'self' blob:; worker-src 'self' blob:; manifest-src 'self'; connect-src 'none'; frame-src 'none'; form-action 'none'; object-src 'none'"

var previewContentTypes = map[string]string{
	"html": "text/html; charset=utf-8", "htm": "text/html; charset=utf-8",
	"css": "text/css; charset=utf-8", "js": "text/javascript; charset=utf-8", "mjs": "text/javascript; charset=utf-8",
	"json": "application/json; charset=utf-8", "svg": "image/svg+xml", "png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg",
	"gif": "image/gif", "webp": "image/webp", "ico": "image/x-icon", "woff": "font/woff", "woff2": "font/woff2",
	"txt": "text/plain; charset=utf-8", "md": "text/plain; charset=utf-8", "pdf": "application/pdf",
	"mp4": "video/mp4", "webm": "video/webm", "mov": "video/quicktime", "m4v": "video/x-m4v",
	"mp3": "audio/mpeg", "wav": "audio/wav", "ogg": "audio/ogg",
}

var previewRangePattern = regexp.MustCompile(`^bytes=(\d*)-(\d*)$`)
var previewRootAttribute = regexp.MustCompile(`(?i)\b(src|href)=(["'])/([^"']*)(["'])`)
var previewBaseElement = regexp.MustCompile(`(?i)<base\b`)
var previewHeadElement = regexp.MustCompile(`(?i)<head\b[^>]*>`)

func previewContentType(path string) string {
	if value := previewContentTypes[strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))]; value != "" {
		return value
	}
	return "application/octet-stream"
}

// As in Source, malformed and multiple ranges are ignored, while valid but
// unsatisfiable single ranges get 416. http.ServeContent has different rules.
func parsePreviewRange(value string, size int64) (start, end int64, partial, unsatisfiable bool) {
	match := previewRangePattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil || match[1] == "" && match[2] == "" {
		return
	}
	if match[1] == "" {
		suffix, _ := strconv.ParseFloat(match[2], 64)
		if math.IsInf(suffix, 0) || suffix <= 0 || size == 0 {
			return 0, 0, false, true
		}
		start = 0
		if suffix < float64(size) {
			start = size - int64(suffix)
		}
		return start, size - 1, true, false
	}
	number, _ := strconv.ParseFloat(match[1], 64)
	if math.IsInf(number, 0) {
		return
	}
	if number >= float64(size) {
		return 0, 0, false, true
	}
	start, end = int64(number), size-1
	if match[2] != "" {
		number, _ = strconv.ParseFloat(match[2], 64)
		if math.IsInf(number, 0) {
			return 0, 0, false, false
		}
		if number < float64(start) {
			return 0, 0, false, true
		}
		if number < float64(end) {
			end = int64(number)
		}
	}
	return start, end, true, false
}

func rewritePreviewHTML(content, base string) string {
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	content = previewRootAttribute.ReplaceAllStringFunc(content, func(value string) string {
		parts := previewRootAttribute.FindStringSubmatch(value)
		if parts[2] != parts[4] || strings.HasPrefix(parts[3], "/") {
			return value
		}
		return parts[1] + "=" + parts[2] + base + parts[3] + parts[4]
	})
	if previewBaseElement.MatchString(content) {
		return content
	}
	location := previewHeadElement.FindStringIndex(content)
	if location == nil {
		return content
	}
	escaped := strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;").Replace(base)
	return content[:location[1]] + `<base href="` + escaped + `">` + content[location[1]:]
}

func previewText(writer http.ResponseWriter, request *http.Request, status int, body string) {
	writer.Header().Set("Content-Type", "text/plain;charset=utf-8")
	writer.WriteHeader(status)
	if request.Method != http.MethodHead {
		_, _ = io.WriteString(writer, body)
	}
}

func (server *Server) previewFilesystem(writer http.ResponseWriter, request *http.Request) {
	pathname := request.URL.EscapedPath()
	rest := strings.TrimPrefix(pathname, "/preview-fs/")
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		previewText(writer, request, 400, "bad request")
		return
	}
	id, idErr := url.PathUnescape(rest[:slash])
	relative, pathErr := url.PathUnescape(rest[slash+1:])
	if idErr != nil || pathErr != nil {
		writeError(writer, requestURIError{})
		return
	}
	info, err := server.findSession(request.Context(), id)
	if err != nil || info.WorkDir == "" {
		previewText(writer, request, 404, "no workdir")
		return
	}
	root := server.filesystemPath(info.WorkDir)
	target := filepath.Join(root, relative)
	if filepath.IsAbs(relative) {
		target = filepath.Clean(relative)
	}
	if !sameOrInsideFilesystemPath(target, root) {
		previewText(writer, request, 403, "forbidden")
		return
	}
	canonicalRoot, rootErr := filepath.EvalSymlinks(root)
	canonicalTarget, targetErr := filepath.EvalSymlinks(target)
	if rootErr != nil || targetErr != nil {
		previewText(writer, request, 404, "not found")
		return
	}
	if !sameOrInsideFilesystemPath(canonicalTarget, canonicalRoot) {
		previewText(writer, request, 403, "forbidden")
		return
	}
	servePreviewFile(writer, request, canonicalTarget, pathname, true)
}

type requestURIError struct{}

func (requestURIError) Error() string { return "URI malformed" }

func (server *Server) localFile(writer http.ResponseWriter, request *http.Request) {
	rest := strings.TrimPrefix(request.URL.EscapedPath(), "/local-file/")
	if rest == "" {
		previewText(writer, request, 400, "bad request")
		return
	}
	parts := strings.Split(rest, "/")
	for i, part := range parts {
		if decoded, err := url.PathUnescape(part); err == nil {
			parts[i] = decoded
		}
	}
	path := strings.Join(parts, "/")
	switch {
	case path == "~":
		path = server.options.HomeDir
	case strings.HasPrefix(path, "~/"):
		path = filepath.Join(server.options.HomeDir, path[2:])
	case len(path) >= 2 && path[1] == ':' && (path[0] >= 'a' && path[0] <= 'z' || path[0] >= 'A' && path[0] <= 'Z') && (len(path) == 2 || path[2] == '/' || path[2] == '\\'):
		// The platform resolver handles drive roots on Windows; on POSIX this
		// intentionally retains Source's cwd-relative treatment of drive text.
	default:
		path = "/" + path
	}
	resolved, err := server.relocatedFilesystemPath(path)
	if err != nil {
		previewText(writer, request, 500, "invalid storage relocation metadata")
		return
	}
	canonical, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		if !server.filesystemAllowed(resolved) {
			previewText(writer, request, 403, "forbidden")
		} else {
			previewText(writer, request, 404, "not found")
		}
		return
	}
	if !server.filesystemAllowed(canonical) {
		previewText(writer, request, 403, "forbidden")
		return
	}
	servePreviewFile(writer, request, canonical, "", false)
}

func servePreviewFile(writer http.ResponseWriter, request *http.Request, path, pathname string, transform bool) {
	file, err := os.Open(path)
	if err != nil {
		previewText(writer, request, 404, "not found")
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		previewText(writer, request, 404, "not found")
		return
	}
	if !stat.Mode().IsRegular() {
		previewText(writer, request, 404, "not a file")
		return
	}
	isHTML := strings.EqualFold(filepath.Ext(path), ".html") || strings.EqualFold(filepath.Ext(path), ".htm")
	if isHTML && transform && len(request.Header.Values("Range")) == 0 {
		if stat.Size() > maxPreviewHTMLBytes {
			previewText(writer, request, 413, "too large")
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, maxPreviewHTMLBytes+1))
		if err != nil {
			writeError(writer, err)
			return
		}
		base := pathname[:strings.LastIndexByte(pathname, '/')+1]
		data = []byte(rewritePreviewHTML(string([]rune(string(data))), base))
		if len(data) > maxPreviewHTMLBytes {
			previewText(writer, request, 413, "too large")
			return
		}
		writer.Header().Set("Content-Type", previewContentType(path))
		writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
		writer.Header().Set("Cache-Control", "no-cache")
		writer.Header().Set("Content-Security-Policy", previewHTMLCSP)
		writer.WriteHeader(200)
		if request.Method != http.MethodHead {
			_, _ = writer.Write(data)
		}
		return
	}
	if stat.Size() > maxPreviewFileBytes {
		previewText(writer, request, 413, "too large")
		return
	}
	if isHTML {
		writer.Header().Set("Content-Security-Policy", previewHTMLCSP)
	}
	writer.Header().Set("Accept-Ranges", "bytes")
	writer.Header().Set("Cache-Control", "no-cache")
	start, end, partial, unsatisfiable := parsePreviewRange(request.Header.Get("Range"), stat.Size())
	if unsatisfiable {
		writer.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(stat.Size(), 10))
		previewText(writer, request, 416, "range not satisfiable")
		return
	}
	writer.Header().Set("Content-Type", previewContentType(path))
	status, length := 200, stat.Size()
	// Bun's file response transport emits 204 for an empty disk blob. This is
	// distinct from transformed HTML, whose response body is a string.
	if length == 0 {
		status = http.StatusNoContent
	}
	if partial {
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			writeError(writer, err)
			return
		}
		status, length = 206, end-start+1
		writer.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(stat.Size(), 10))
	}
	writer.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	writer.WriteHeader(status)
	if request.Method != http.MethodHead {
		_, _ = io.CopyN(writer, file, length)
	}
}
