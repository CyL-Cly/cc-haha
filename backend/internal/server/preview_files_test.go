package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
)

type previewObservation struct {
	Status  int                `json:"status"`
	Headers map[string]*string `json:"headers"`
	Body    string             `json:"body"`
}
type previewCase struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
}

func TestPreviewAndLocalFilesActualSourceHTTP(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	_, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	html := `<!doctype html><HEAD data-test="a"><title>实际文件</title></HEAD><body><script src="/app.js"></script><a href='/notes.md'>link</a><img src="//other/image.png"><a href="relative.html">relative</a></body>`
	files := map[string][]byte{
		"目录 空格/index.html": []byte(html), "目录 空格/app.js": []byte("document.body.dataset.loaded='ok'"),
		"目录 空格/base.htm": []byte(`<head><base href="/existing/"></head><img SRC='/x.png'>`),
		"clip.mp4":       []byte("0123456789abcdefghijklmnopqrstuvwxyz"), "empty.txt": {}, "notes.md": []byte("actual markdown\n"), "opaque.unknown": {0, 0xff, 0x81, 10},
	}
	for name, data := range files {
		path := filepath.Join(options.WorkDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	escape := filepath.Join(options.HomeDir, "outside.txt")
	if err := os.WriteFile(escape, []byte("outside workspace"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escape, filepath.Join(options.WorkDir, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	prefix := "/preview-fs/" + id + "/"
	local := "/local-file/" + strings.TrimPrefix(filepath.ToSlash(options.WorkDir), "/") + "/"
	cases := []previewCase{
		{Path: prefix + "%E7%9B%AE%E5%BD%95%20%E7%A9%BA%E6%A0%BC/index.html"},
		{Path: prefix + "%E7%9B%AE%E5%BD%95%20%E7%A9%BA%E6%A0%BC/app.js"},
		{Path: prefix + "%E7%9B%AE%E5%BD%95%20%E7%A9%BA%E6%A0%BC/base.htm"},
		{Path: prefix + "notes.md"}, {Path: prefix + "opaque.unknown"}, {Path: prefix + "empty.txt"},
		{Path: prefix + "missing.png"}, {Path: prefix}, {Path: prefix + "escape.txt"},
		{Path: prefix + "%2e%2e%2foutside.txt"}, {Path: prefix + "%2Fetc%2Fpasswd"},
		{Path: "/preview-fs/no-such-session/notes.md"}, {Path: "/preview-fs/" + id},
		{Path: local + "%E7%9B%AE%E5%BD%95%20%E7%A9%BA%E6%A0%BC/index.html"},
		{Path: "/local-file/~/work/notes.md"}, {Path: "/local-file/~/work/missing.txt"},
		{Path: "/local-file/etc/passwd"}, {Path: "/local-file/etc/missing-private-file"}, {Path: "/local-file/"},
		{Path: local + "bad%25ZZ"}, {Path: local + "escape.txt"},
	}
	for _, header := range []string{"bytes=0-9", "bytes=10-", "bytes=-5", "bytes=-999", "bytes=30-99", "bytes=100-", "bytes=9-4", "bytes=-0", "bytes=-", "bytes=0-1,2-3", "invalid", " bytes=1-2 ", "bytes=" + strings.Repeat("9", 320) + "-", "bytes=0-" + strings.Repeat("9", 320)} {
		for _, path := range []string{prefix + "clip.mp4", local + "clip.mp4"} {
			cases = append(cases, previewCase{Path: path, Headers: map[string]string{"Range": header}})
		}
	}
	for _, path := range []string{prefix + "目录%20空格/index.html", prefix + "empty.txt"} {
		cases = append(cases, previewCase{Path: path, Headers: map[string]string{"Range": "bytes=0-9"}})
	}
	for _, name := range []string{"large.html", "large.mp4"} {
		file, err := os.Create(filepath.Join(options.WorkDir, name))
		if err != nil {
			t.Fatal(err)
		}
		size := int64(maxPreviewHTMLBytes + 1)
		if name == "large.mp4" {
			size = maxPreviewFileBytes + 1
		}
		if err = file.Truncate(size); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		cases = append(cases, previewCase{Path: prefix + name})
	}
	observations := []previewObservation{}
	for _, row := range cases {
		req, err := http.NewRequest("GET", host.URL+row.Path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range row.Headers {
			req.Header.Set(k, v)
		}
		response, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		headers := map[string]*string{}
		for _, name := range []string{"content-type", "content-range", "accept-ranges", "content-length", "cache-control", "content-security-policy"} {
			if value := response.Header.Get(name); value != "" {
				headers[name] = &value
			} else {
				headers[name] = nil
			}
		}
		observations = append(observations, previewObservation{response.StatusCode, headers, base64.StdEncoding.EncodeToString(data)})
	}
	if observations[0].Status != 200 || !strings.Contains(string(decodePreviewBody(t, observations[0].Body)), `<base href="`+prefix) {
		t.Fatal("workspace HTML not transformed", observations[0])
	}
	if observations[8].Status != 403 || observations[9].Status != 403 || observations[16].Status != 403 {
		t.Fatal("sandbox escape", observations)
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Actual Source comparison requires pinned Bun")
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-preview-files.fixture.ts"))
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = options.HomeDir
	cmd.Env = []string{"PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "HOME=" + options.HomeDir, "USERPROFILE=" + options.HomeDir, "CLAUDE_CONFIG_DIR=" + options.ConfigDir, "TMPDIR=" + options.HomeDir, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1"}
	input, _ := json.Marshal(map[string]any{"sessions": map[string]string{id: options.WorkDir}, "cases": cases})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source files: %v\n%s", err, stderr.String())
	}
	var expected []previewObservation
	if err = json.Unmarshal(source, &expected); err != nil {
		t.Fatal(err, string(source))
	}
	for i, actual := range observations {
		if !reflect.DeepEqual(actual, expected[i]) {
			t.Fatalf("case %d %s %v\nSource %+v\nGo %+v", i, cases[i].Path, cases[i].Headers, expected[i], actual)
		}
	}
}

func decodePreviewBody(t *testing.T, value string) []byte {
	t.Helper()
	result, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPreviewTransportAuthenticationAndHead(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	if err := os.WriteFile(filepath.Join(options.WorkDir, "image.png"), []byte("actual-image"), 0600); err != nil {
		t.Fatal(err)
	}
	// Use the same local authentication as the actual Electron sidecar.
	server.options.AuthRequired = true
	server.options.Authorization = access.Authorization{LocalToken: "fixture-token"}
	for _, path := range []string{"/preview-fs/" + id + "/image.png", "/local-file/~/work/image.png"} {
		for _, token := range []string{"", "fixture-token"} {
			req, _ := http.NewRequest("HEAD", host.URL+path, nil)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			res, err := host.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			want := 401
			if token != "" {
				want = 200
			}
			if res.StatusCode != want || len(body) != 0 {
				t.Fatalf("%s auth %v: %d %q", path, token != "", res.StatusCode, body)
			}
		}
	}
	// A discarded path prefix cannot accidentally expose a file as SPA HTML.
	req := httptest.NewRequest("GET", "/local-file/~/work/image.png", nil)
	req.Header.Set("Authorization", "Bearer fixture-token")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != 200 || recorder.Body.String() != "actual-image" {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
}
