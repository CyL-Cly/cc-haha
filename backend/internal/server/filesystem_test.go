package server

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFilesystemNativeBrowseImageAndSessionRootRegistration(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	server, host := testHost(t, options)
	for _, name := range []string{".claude", ".git", ".SVN", "zebra"} {
		if err := os.MkdirAll(filepath.Join(options.HomeDir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	image := filepath.Join(options.HomeDir, "chart.PNG")
	const bytes = "real private image bytes"
	if err := os.WriteFile(image, []byte(bytes), 0600); err != nil {
		t.Fatal(err)
	}
	raw := requestJSON(t, host, "GET", "/api/filesystem/browse?includeFiles=true", "", 200)
	var listing struct {
		CurrentPath string
		ParentPath  string
		Entries     []filesystemEntry
	}
	if json.Unmarshal(raw, &listing) != nil || listing.CurrentPath != canonicalAccessPath(options.HomeDir) {
		t.Fatal(string(raw))
	}
	found := map[string]filesystemEntry{}
	for _, entry := range listing.Entries {
		found[entry.Name] = entry
	}
	if _, exists := found[".git"]; exists {
		t.Fatal("VCS metadata shown")
	}
	if _, exists := found[".SVN"]; exists || !found[".claude"].IsDirectory || found["chart.PNG"].IsDirectory || found["chart.PNG"].RelativePath != "chart.PNG" {
		t.Fatal("dot entries or file fields differ", found)
	}
	for _, path := range []string{image, "~/chart.PNG"} {
		response, err := host.Client().Get(host.URL + "/api/filesystem/file?path=" + url.QueryEscape(path))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 || string(data) != bytes || response.Header.Get("Content-Type") != "image/png" || response.Header.Get("Cache-Control") != "private, no-cache" {
			t.Fatal("native image response", response.StatusCode, string(data), err)
		}
	}
	requestJSON(t, host, "GET", "/api/filesystem/file", "", 400)
	requestJSON(t, host, "GET", "/api/filesystem/browse?path="+url.QueryEscape(image), "", 400)
	requestJSON(t, host, "GET", "/api/filesystem/file?path="+url.QueryEscape(filepath.Join(options.HomeDir, "missing.png")), "", 404)
	requestJSON(t, host, "GET", "/api/filesystem/browse?search=chart", "", 501)
	requestJSON(t, host, "GET", "/api/filesystem/unknown", "", 404)
	external := t.TempDir()
	if server.filesystemAllowed(external) && runtime.GOOS != "windows" {
		var err error
		external, err = os.MkdirTemp("/var/tmp", "cc-haha-filesystem-root-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(external) })
	}
	if !server.filesystemAllowed(external) {
		outside := filepath.Join(external, "outside.png")
		if err := os.WriteFile(outside, []byte("registered bytes"), 0600); err != nil {
			t.Fatal(err)
		}
		requestJSON(t, host, "GET", "/api/filesystem/browse?path="+url.QueryEscape(external), "", 403)
		requestJSON(t, host, "GET", "/api/filesystem/file?path="+url.QueryEscape(outside), "", 403)
		link := filepath.Join(options.HomeDir, "escape")
		if err := os.Symlink(external, link); err == nil {
			requestJSON(t, host, "GET", "/api/filesystem/browse?path="+url.QueryEscape(link), "", 403)
			requestJSON(t, host, "GET", "/api/filesystem/file?path="+url.QueryEscape(filepath.Join(link, "missing.png")), "", 403)
		}
		createTestSession(t, host, external)
		requestJSON(t, host, "GET", "/api/filesystem/browse?path="+url.QueryEscape(external), "", 200)
		response, err := host.Client().Get(host.URL + "/api/filesystem/file?path=" + url.QueryEscape(outside))
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("actual session creation did not register root", response.StatusCode)
		}
	}
}

func TestFilesystemLimitsAndMissingAncestorCanonicalization(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	_, host := testHost(t, options)
	image := filepath.Join(options.HomeDir, "large.png")
	file, err := os.Create(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(50*1024*1024 + 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	requestJSON(t, host, "GET", "/api/filesystem/file?path="+url.QueryEscape(image), "", 400)
	directory := filepath.Join(options.HomeDir, "dir.png")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, host, "GET", "/api/filesystem/file?path="+url.QueryEscape(directory), "", 400)
	if got := canonicalAccessPath(filepath.Join(options.HomeDir, "not-yet", "image.png")); got != filepath.Join(canonicalAccessPath(options.HomeDir), "not-yet", "image.png") {
		t.Fatal(got)
	}
}
