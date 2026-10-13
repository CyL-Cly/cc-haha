package server

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedRelocationFilesystemAndLocalPreviewNeverFallBackToOldBytes(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	oldRoot := filepath.Join(options.HomeDir, "previous-config")
	options.ConfigDir = filepath.Join(options.HomeDir, "moved-config")
	metadata := filepath.Join(options.ConfigDir, "cc-haha", "storage-relocations.json")
	oldPath := filepath.Join(oldRoot, "uploads", "old-session", "reference.png")
	newPath := filepath.Join(options.ConfigDir, "uploads", "old-session", "reference.png")
	for _, path := range []string{oldPath, newPath, metadata} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(oldPath, []byte("OLD_BYTES_MUST_NOT_BE_READ"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("ACTUAL_MOVED_REFERENCE"), 0600); err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(map[string]any{"version": 1, "previousRoots": []string{oldRoot}, "future": map[string]any{"untouched": true}})
	if err := os.WriteFile(metadata, document, 0600); err != nil {
		t.Fatal(err)
	}
	_, host := testHost(t, options)
	paths := []string{"/api/filesystem/file?path=" + url.QueryEscape(oldPath), "/local-file/" + url.PathEscape(oldPath)}
	read := func(path string, want int) string {
		t.Helper()
		response, err := host.Client().Get(host.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != want {
			t.Fatalf("%s status%d want%d %s %v", path, response.StatusCode, want, raw, err)
		}
		return string(raw)
	}
	for _, path := range paths {
		if got := read(path, 200); got != "ACTUAL_MOVED_REFERENCE" {
			t.Fatal("old attachment byte leak", got)
		}
	}
	raw := requestJSON(t, host, "GET", "/api/filesystem/browse?path="+url.QueryEscape(filepath.Dir(oldPath))+"&includeFiles=true", "", 200)
	var listing struct{ CurrentPath string }
	if json.Unmarshal(raw, &listing) != nil || listing.CurrentPath != canonicalAccessPath(filepath.Dir(newPath)) {
		t.Fatal("browse did not resolve managed root", string(raw))
	}
	// The old file is deliberately still present. Broken metadata and a missing
	// migrated target must fail, instead of silently reviving that stale file.
	if err := os.WriteFile(metadata, []byte(`{"version":2,"previousRoots":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		read(path, 500)
	}
	if err := os.WriteFile(metadata, document, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(newPath); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		read(path, 404)
	}
	old, _ := os.ReadFile(oldPath)
	if string(old) != "OLD_BYTES_MUST_NOT_BE_READ" {
		t.Fatal("old managed file mutated")
	}
}
