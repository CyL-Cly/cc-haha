package server

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var staticMIME = map[string]string{
	".css": "text/css; charset=utf-8", ".gif": "image/gif", ".html": "text/html; charset=utf-8", ".ico": "image/x-icon", ".js": "text/javascript; charset=utf-8", ".json": "application/json; charset=utf-8", ".map": "application/json; charset=utf-8", ".mjs": "text/javascript; charset=utf-8", ".png": "image/png", ".svg": "image/svg+xml", ".txt": "text/plain; charset=utf-8", ".wasm": "application/wasm", ".webp": "image/webp", ".woff": "font/woff", ".woff2": "font/woff2",
}

func unpackedASAR(path string) string {
	for at := strings.Index(path, ".asar"); at >= 0; {
		end := at + len(".asar")
		if end == len(path) || path[end] == '/' || path[end] == '\\' {
			return path[:end] + ".unpacked" + path[end:]
		}
		next := strings.Index(path[end:], ".asar")
		if next < 0 {
			break
		}
		at = end + next
	}
	return ""
}
func regularFile(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular()
}
func (server *Server) staticRoot() string {
	env := map[string]string{}
	if server.options.Environment != nil {
		env = server.options.Environment()
	}
	dist := server.options.H5DistDir
	if dist == "" {
		dist = env["CLAUDE_H5_DIST_DIR"]
	}
	candidates := []string{dist, unpackedASAR(dist)}
	if root := server.options.AppRoot; root != "" {
		candidates = append(candidates, filepath.Join(root, "..", "Resources", "_up_", "dist"), unpackedASAR(filepath.Join(root, "dist")), filepath.Join(root, "..", "Resources", "dist"), filepath.Join(root, "dist"))
	}
	candidates = append(candidates, filepath.Join(server.options.WorkDir, "desktop", "dist"), filepath.Join(server.options.WorkDir, "dist"))
	for _, candidate := range candidates {
		if candidate != "" && regularFile(filepath.Join(candidate, "index.html")) {
			absolute, err := filepath.Abs(candidate)
			if err == nil {
				return absolute
			}
		}
	}
	return ""
}

func (server *Server) static(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(writer, "Not Found")
		return
	}
	root := server.staticRoot()
	if root == "" {
		http.NotFound(writer, request)
		return
	}
	decoded, err := url.PathUnescape(request.URL.EscapedPath())
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	relative := strings.TrimLeft(decoded, "/")
	if relative == "" {
		relative = "index.html"
	}
	requested := filepath.Join(root, relative)
	relation, err := filepath.Rel(root, requested)
	if err != nil || strings.HasPrefix(relation, "..") || filepath.IsAbs(relation) {
		http.NotFound(writer, request)
		return
	}
	path := requested
	if !regularFile(path) {
		path = filepath.Join(requested, "index.html")
		if !regularFile(path) {
			if filepath.Ext(requested) != "" {
				http.NotFound(writer, request)
				return
			}
			path = filepath.Join(root, "index.html")
		}
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		http.NotFound(writer, request)
		return
	}
	mime := staticMIME[strings.ToLower(filepath.Ext(path))]
	if mime == "" {
		mime = "application/octet-stream"
	}
	writer.Header().Set("Content-Type", mime)
	writer.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	cache := "no-store"
	if strings.HasPrefix(request.URL.Path, "/assets/") {
		cache = "public, max-age=31536000, immutable"
	}
	writer.Header().Set("Cache-Control", cache)
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = io.Copy(writer, file)
	}
}
