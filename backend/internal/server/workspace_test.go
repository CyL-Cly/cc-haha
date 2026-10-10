package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorkspaceNativeTreeAndPreviewPreserveSourceFileBoundaries(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	options.RespectGitignore = func(context.Context) (bool, error) { return true, nil }
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	base := "/api/sessions/" + id + "/workspace/"
	for _, name := range []string{"folder", ".git", ".HG", "..notes"} {
		if err := os.Mkdir(filepath.Join(options.WorkDir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string][]byte{"z.txt": []byte("actual text\n"), "a.go": []byte("package actual\n"), "null.bin": {0, 1, 2}, "picture.png": {0, 1, 2, 3}, ".pdf": []byte("a dotfile is text"), "actual.pdf": []byte("%PDF-actual\n"), "invalid.txt": {0xef, 0xbf, 0xbd, 0xe2, 0x82}} {
		if err := os.WriteFile(filepath.Join(options.WorkDir, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw := requestJSON(t, host, "GET", base+"tree", "", 200)
	body := historyRecord(raw)
	var entries []workspaceTreeEntry
	_ = json.Unmarshal(body["entries"], &entries)
	if stringField(body, "state") != "ok" || len(entries) != 9 || entries[0].Name != "..notes" || !entries[0].IsDirectory || entries[1].Name != "folder" || strings.Contains(string(raw), `.git`) || strings.Contains(string(raw), `.HG`) {
		t.Fatal(string(raw))
	}
	raw = requestJSON(t, host, "GET", base+"tree?path=..notes", "", 200)
	if stringField(historyRecord(raw), "state") != "ok" {
		t.Fatal(string(raw))
	}
	raw = requestJSON(t, host, "GET", base+"file?path=a.go", "", 200)
	body = historyRecord(raw)
	if stringField(body, "content") != "package actual\n" || stringField(body, "language") != "go" || stringField(body, "previewType") != "text" || string(body["readBytes"]) != "15" || string(body["truncated"]) != "false" {
		t.Fatal(string(raw))
	}
	raw = requestJSON(t, host, "GET", base+"file?path=null.bin", "", 200)
	if stringField(historyRecord(raw), "state") != "binary" {
		t.Fatal(string(raw))
	}
	raw = requestJSON(t, host, "GET", base+"file?path=picture.png", "", 200)
	body = historyRecord(raw)
	if stringField(body, "dataUrl") != "data:image/png;base64,AAECAw==" || stringField(body, "language") != "image" {
		t.Fatal(string(raw))
	}
	raw = requestJSON(t, host, "GET", base+"file?path=.pdf", "", 200)
	if stringField(historyRecord(raw), "previewType") != "text" {
		t.Fatal(string(raw))
	}
	raw = requestJSON(t, host, "GET", base+"file?path=invalid.txt", "", 200)
	if stringField(historyRecord(raw), "content") != "\ufffd\ufffd" {
		t.Fatal(string(raw))
	}
	raw = requestJSON(t, host, "GET", base+"file?path=actual.pdf", "", 200)
	body = historyRecord(raw)
	if stringField(body, "previewType") != "pdf" || len(body["content"]) != 0 || stringField(body, "version") == "" {
		t.Fatal(string(raw))
	}
	response, err := host.Client().Get(host.URL + base + "raw?path=actual.pdf")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(data) != "%PDF-actual\n" || response.Header.Get("Content-Type") != "application/pdf" || response.Header.Get("Cache-Control") != "private, no-cache" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(response.StatusCode, string(data), response.Header, err)
	}
	requestJSON(t, host, "GET", base+"raw?path=z.txt", "", 415)
	requestJSON(t, host, "GET", base+"raw?path=missing.pdf", "", 404)
	requestJSON(t, host, "GET", base+"file", "", 400)
	requestJSON(t, host, "GET", base+"file?path=missing.txt", "", 200)
	requestJSON(t, host, "GET", base+"file?path=folder/missing.txt", "", 200)
	requestJSON(t, host, "GET", base+"file?path=../outside.txt", "", 403)
	outside := filepath.Join(options.HomeDir, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside actual"), 0600); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, host, "GET", base+"file?path="+url.QueryEscape(outside), "", 403)
	server.RegisterChangedFileAccessRoot(outside, options.WorkDir)
	raw = requestJSON(t, host, "GET", base+"file?path="+url.QueryEscape(outside), "", 200)
	if stringField(historyRecord(raw), "path") != filepath.ToSlash(outside) || stringField(historyRecord(raw), "content") != "outside actual" {
		t.Fatal(string(raw))
	}
	if runtime.GOOS != "windows" {
		other := t.TempDir()
		target := filepath.Join(other, "private.txt")
		if err := os.WriteFile(target, []byte("not registered"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(other, filepath.Join(options.WorkDir, "escape")); err != nil {
			t.Fatal(err)
		}
		requestJSON(t, host, "GET", base+"file?path=escape/private.txt", "", 403)
		requestJSON(t, host, "GET", base+"tree?path=escape/missing", "", 403)
	}
}

func TestWorkspaceSearchReturnsFilesOnlyAndPreservesThe201stOverflowEvidence(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	options.RespectGitignore = func(context.Context) (bool, error) { return true, nil }
	_, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	base := "/api/sessions/" + id + "/workspace/"
	for i := 0; i < 202; i++ {
		if err := os.WriteFile(filepath.Join(options.WorkDir, fmt.Sprintf("match-%03d.txt", i)), []byte("actual"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(options.WorkDir, "match-folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.WorkDir, ".ignore"), []byte("match-000.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := requestJSON(t, host, "GET", base+"search?query=%20match%20", "", 200)
	body := historyRecord(raw)
	var entries []workspaceTreeEntry
	_ = json.Unmarshal(body["entries"], &entries)
	if stringField(body, "query") != "match" || string(body["truncated"]) != "true" || len(entries) != 200 || entries[0].Name != "match-001.txt" {
		t.Fatal(string(raw))
	}
	for _, entry := range entries {
		if entry.IsDirectory {
			t.Fatal("workspace search included directory", entry)
		}
	}
	requestJSON(t, host, "GET", base+"search?query=%20", "", 400)
	requestJSON(t, host, "GET", base+"unknown", "", 404)
	requestJSON(t, host, "GET", base+"watch", "", 400)
	requestJSON(t, host, "GET", base+"status", "", 200)
	diff := requestJSON(t, host, "GET", base+"diff?path=match-001.txt", "", 200)
	if stringField(historyRecord(diff), "state") != "not_git_repo" {
		t.Fatal(string(diff))
	}
}

func TestWorkspaceActualByteBudgetsUseTextPrefixAndDocumentMetadata(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	_, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	base := "/api/sessions/" + id + "/workspace/"
	if err := os.WriteFile(filepath.Join(options.WorkDir, "large.txt"), []byte(strings.Repeat("actual", 180000)), 0600); err != nil {
		t.Fatal(err)
	}
	raw := requestJSON(t, host, "GET", base+"file?path=large.txt", "", 200)
	body := historyRecord(raw)
	if string(body["readBytes"]) != "1048576" || string(body["truncated"]) != "true" || len(stringField(body, "content")) != 1024*1024 {
		t.Fatal(string(body["readBytes"]), string(body["truncated"]))
	}
	file, err := os.OpenFile(filepath.Join(options.WorkDir, "huge.docx"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(30*1024*1024 + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	raw = requestJSON(t, host, "GET", base+"file?path=huge.docx", "", 200)
	body = historyRecord(raw)
	if stringField(body, "state") != "too_large" || stringField(body, "previewType") != "docx" || len(body["content"]) != 0 {
		t.Fatal(string(raw))
	}
	requestJSON(t, host, "GET", base+"raw?path=huge.docx", "", 413)
}
