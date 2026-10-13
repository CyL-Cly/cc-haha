package imagegeneration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActualImageEditReadsMigratedReferenceBeforePhysicalRequest(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldRoot := filepath.Join(home, "previous")
	config := filepath.Join(home, "moved")
	oldPath := filepath.Join(oldRoot, "uploads", "saved-session", "reference.png")
	newPath := filepath.Join(config, "uploads", "saved-session", "reference.png")
	metadata := filepath.Join(config, "cc-haha", "storage-relocations.json")
	for _, path := range []string{oldPath, newPath, metadata} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl6Xh0AAAAASUVORK5CYII=")
	if err := os.WriteFile(oldPath, []byte("STALE_OLD_INPUT"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, png, 0600); err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(map[string]any{"version": 1, "previousRoots": []string{oldRoot}})
	if err := os.WriteFile(metadata, document, 0600); err != nil {
		t.Fatal(err)
	}
	posts, reads := 0, 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		_, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil {
			t.Error(e)
			return
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
		files := 0
		for {
			part, e := reader.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Error(e)
				return
			}
			data, _ := io.ReadAll(part)
			if part.FileName() != "" {
				files++
				if !bytes.Equal(data, png) {
					t.Error("physical image edit sent stale bytes")
				}
			}
		}
		if files != 1 {
			t.Error("physical image edit file count", files)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(png)}}})
	}))
	defer peer.Close()
	service, err := New(Options{Files: NativeFiles{Check: func(op, path string) error {
		if op == "image-read" {
			reads++
			if path != newPath {
				t.Error("reader used wrong managed path", path)
			}
		}
		return nil
	}}, Environment: func() map[string]string { return map[string]string{} }, ConfigHome: config, SessionID: func() string { return "saved-session" }, NowMillis: func() int64 { return 1700000000000 }, UUID: func() string { return "private-image-output" }, Fetch: func(ctx context.Context, request Request) (Response, error) {
		r, e := http.NewRequestWithContext(ctx, request.Method, request.URL, bytes.NewReader(request.Body))
		if e != nil {
			return Response{}, e
		}
		r.Header = request.Headers.Clone()
		response, e := peer.Client().Do(r)
		if e != nil {
			return Response{}, e
		}
		defer response.Body.Close()
		data, e := io.ReadAll(response.Body)
		return Response{Status: response.StatusCode, Headers: response.Header, Body: data, ReadError: e}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	in := inputObject(map[string]any{"prompt": "edit the provided image", "count": 1, "referenced_image_paths": []string{oldPath}})
	provider := Config{Kind: "openai_images", ProviderID: "private-test-provider", Model: "private-image-model", BaseURL: peer.URL + "/v1", APIKey: "fake"}
	if _, err = service.Generate(context.Background(), in, provider); err != nil || posts != 1 || reads != 1 {
		t.Fatal("moved edit failed", err, posts, reads)
	}
	if err = os.WriteFile(metadata, []byte(`{"version":7,"previousRoots":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Generate(context.Background(), in, provider); err == nil || !strings.Contains(err.Error(), "relocation metadata") || posts != 1 || reads != 1 {
		t.Fatal("broken metadata reached reader/provider", err, posts, reads)
	}
	if err = os.WriteFile(metadata, document, 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "external.png")
	if err = os.WriteFile(outside, png, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(newPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, newPath); err == nil {
		if _, err = service.Generate(context.Background(), in, provider); err == nil || posts != 1 || reads != 1 {
			t.Fatal("relocated symlink escaped input authorization", err, posts, reads)
		}
	}
	old, _ := os.ReadFile(oldPath)
	if string(old) != "STALE_OLD_INPUT" {
		t.Fatal("original input mutated")
	}
}
