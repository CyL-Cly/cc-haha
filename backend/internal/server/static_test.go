package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticReactSPAFallbackAssetsHeadAndContainment(t *testing.T) {
	options := testOptions(t, func(_ context.Context, _ SessionOptions, _ Emit) (LiveSession, error) {
		return &transportSession{}, nil
	})
	options.AppRoot = filepath.Join(options.HomeDir, "app.asar")
	dist := filepath.Join(options.HomeDir, "app.asar.unpacked", "dist")
	for path, contents := range map[string]string{"index.html": "fixture-react-index", "assets/main.mjs": "fixture-module", "assets/engine.wasm": "fixture-wasm", "nested/index.html": "fixture-nested", "folder/data.bin": "fixture-bin"} {
		full := filepath.Join(dist, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server, host := testHost(t, options)
	publicBootstrap := httptest.NewRequest("GET", "http://fixture.invalid/", nil)
	publicBootstrap.Header.Set("Origin", "https://untrusted.invalid")
	publicBootstrap.RemoteAddr = "203.0.113.20:5000"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, publicBootstrap)
	if recorder.Code != 200 || recorder.Body.String() != "fixture-react-index" {
		t.Fatal("public React bootstrap required API credentials")
	}
	for _, row := range []struct {
		method, path, body, mime, cache string
		status                          int
	}{
		{"GET", "/", "fixture-react-index", "text/html; charset=utf-8", "no-store", 200},
		{"GET", "/chat/session-fixture", "fixture-react-index", "text/html; charset=utf-8", "no-store", 200},
		{"GET", "/nested", "fixture-nested", "text/html; charset=utf-8", "no-store", 200},
		{"GET", "/assets/main.mjs", "fixture-module", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable", 200},
		{"HEAD", "/assets/engine.wasm", "", "application/wasm", "public, max-age=31536000, immutable", 200},
		{"GET", "/folder/data.bin", "fixture-bin", "application/octet-stream", "no-store", 200},
		{"GET", "/assets/missing.mjs", "", "", "", 404},
		{"GET", "/%2e%2e/secret", "", "", "", 404},
		{"GET", "/%2e%2e-hidden", "", "", "", 404},
	} {
		t.Run(row.method+row.path, func(t *testing.T) {
			request, err := http.NewRequest(row.method, host.URL+row.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := host.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != row.status {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
			if row.status == 200 && (string(body) != row.body || response.Header.Get("Content-Type") != row.mime || response.Header.Get("Cache-Control") != row.cache) {
				t.Fatalf("body/headers=%s %v", body, response.Header)
			}
			if row.method == "HEAD" && response.ContentLength != int64(len("fixture-wasm")) {
				t.Fatal("HEAD lost asset size")
			}
		})
	}
}
