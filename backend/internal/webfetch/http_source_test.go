package webfetch

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"github.com/andybalholm/brotli"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixtureRoute struct {
	base      string
	transport http.RoundTripper
}

func (r fixtureRoute) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	url := *request.URL
	originalHost := url.Hostname()
	base := strings.TrimPrefix(r.base, "http://")
	url.Scheme = "http"
	url.Host = base
	copy.URL = &url
	copy.Host = base
	copy.Header = copy.Header.Clone()
	copy.Header.Set("X-Fixture-Original-Host", originalHost)
	return r.transport.RoundTrip(copy)
}
func TestNativeFetchHTTPRedirectCacheAndBinaryMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/web/domain_info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"can_fetch":true}`))
		case "/gzip", "/deflate", "/brotli":
			w.Header().Set("Content-Type", "text/plain")
			var data bytes.Buffer
			switch r.URL.Path {
			case "/gzip":
				w.Header().Set("Content-Encoding", "gzip")
				writer := gzip.NewWriter(&data)
				_, _ = writer.Write([]byte("compressed real bytes"))
				_ = writer.Close()
			case "/deflate":
				w.Header().Set("Content-Encoding", "deflate")
				writer := zlib.NewWriter(&data)
				_, _ = writer.Write([]byte("compressed real bytes"))
				_ = writer.Close()
			case "/brotli":
				w.Header().Set("Content-Encoding", "br")
				writer := brotli.NewWriter(&data)
				_, _ = writer.Write([]byte("compressed real bytes"))
				_ = writer.Close()
			}
			_, _ = w.Write(data.Bytes())
		case "/text":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("Raw body\n"))
		case "/html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<h1>Fixture</h1><p>body <strong>bold</strong></p>"))
		case "/empty":
			w.Header().Set("Content-Type", "text/plain")
		case "/binary":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-fixture\x00\xff"))
		case "/same":
			w.Header().Set("Location", "/text")
			w.WriteHeader(301)
		case "/cross":
			w.Header().Set("Location", "https://two.fixture/other")
			w.WriteHeader(302)
		case "/missing":
			w.WriteHeader(307)
		case "/loop":
			w.Header().Set("Location", "/loop")
			w.WriteHeader(308)
		case "/egress":
			w.Header().Set("X-Proxy-Error", "blocked-by-allowlist")
			w.WriteHeader(403)
		default:
			http.Error(w, "not found", 404)
		}
	}))
	defer server.Close()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	cases := []map[string]any{{"url": "http://one.fixture:$PORT/text", "clear": true}, {"url": "http://one.fixture:$PORT/text"}, {"url": "https://one.fixture:$PORT/html", "settings": map[string]any{"skipWebFetchPreflight": true}}, {"url": "https://one.fixture:$PORT/empty", "desktop": true}, {"url": "https://one.fixture:$PORT/same"}, {"url": "https://one.fixture:$PORT/cross"}, {"url": "https://one.fixture:$PORT/missing"}, {"url": "https://one.fixture:$PORT/loop"}, {"url": "https://one.fixture:$PORT/egress"}, {"url": "https://one.fixture:$PORT/gzip"}, {"url": "https://one.fixture:$PORT/deflate"}, {"url": "https://one.fixture:$PORT/brotli"}, {"url": "https://one.fixture:$PORT/notfound"}, {"url": "https://one.fixture:$PORT/binary"}, {"url": "ftp://one.fixture/file"}, {"url": "https://localhost/text"}, {"url": "https://user:secret@one.fixture/text"}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-webfetch-http.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WEBFETCH_HTTP_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "origin": server.URL, "now": 1770000000000, "cases": cases})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source HTTP %v %.2400s", err, stderr.String())
	}
	var source struct {
		Rows         []map[string]any
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source HTTP escaped %v %.1000s stderr%.1000s", source.Capabilities.Blocked, out, stderr.String())
	}
	settings := Settings{}
	var persisted []byte
	service, err := New(Options{HTTPClient: &http.Client{Transport: fixtureRoute{server.URL, http.DefaultTransport}}, Settings: func(context.Context) (Settings, error) { return settings, nil }, Now: func() time.Time { return time.UnixMilli(1770000000000) }, UserAgent: "Claude-User (claude-code/999.0.0-local; +https://support.anthropic.com/)", PersistID: func() string { return "ID" }, PersistBinary: func(_ context.Context, data []byte, _ string, _ string) (Persisted, error) {
		persisted = append([]byte{}, data...)
		return Persisted{Path: "webfetch-ID.pdf", Size: len(data)}, nil
	}, ApplyPrompt: func(context.Context, json.RawMessage, bool) (json.RawMessage, error) {
		t.Fatal("Fetch utility called model")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	for i, row := range cases {
		if row["clear"] == true {
			service.ClearCache()
		}
		settings = Settings{}
		if row["desktop"] == true {
			settings.DesktopServerURL = "http://desktop-fixture"
		}
		if custom, ok := row["settings"].(map[string]any); ok {
			value := custom["skipWebFetchPreflight"].(bool)
			settings.SkipPreflight = &value
		}
		url := strings.ReplaceAll(row["url"].(string), "$PORT", port)
		result, err := service.Fetch(context.Background(), url)
		want := source.Rows[i]
		if sourceError, ok := want["error"].(string); ok {
			if err == nil || err.Error() != sourceError {
				t.Errorf("case%d Go %v Source%v", i, err, want)
			}
			continue
		}
		if err != nil {
			t.Fatalf("case%d Go error%v Source%v", i, err, want)
		}
		if result.Redirect != nil {
			data, _ := json.Marshal(result.Redirect)
			var actual map[string]any
			_ = json.Unmarshal(data, &actual)
			a, _ := json.Marshal(actual)
			b, _ := json.Marshal(want)
			if string(a) != string(b) {
				t.Errorf("redirect case%d Go%s Source%s", i, a, b)
			}
			continue
		}
		f := result.Fetched
		actual := map[string]any{"content": f.Content, "bytes": f.Bytes, "code": f.Code, "codeText": f.CodeText, "contentType": f.ContentType}
		if f.PersistedPath != "" {
			want["persistedPath"] = filepath.Base(want["persistedPath"].(string))
			actual["persistedPath"] = f.PersistedPath
			actual["persistedSize"] = f.PersistedSize
			numbers := []int{}
			for _, b := range persisted {
				numbers = append(numbers, int(b))
			}
			actual["persistedBytes"] = numbers
		}
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			t.Errorf("case%d Go%s Source%s", i, a, b)
		}
	}
}
