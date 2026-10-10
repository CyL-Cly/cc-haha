package imruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/klauspost/compress/zstd"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDingTalkOriginalTokenCoercionCacheErrorsAndNoSingleflight(t *testing.T) {
	for _, expiry := range []json.RawMessage{nil, raw(nil), raw(0), raw(-1), raw(false), raw(true), raw("60"), raw("invalid"), json.RawMessage("1e400"), raw("0x20000000000000000"), raw([]any{7200})} {
		t.Run(string(expiry), func(t *testing.T) {
			var mu sync.Mutex
			count := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				count++
				mu.Unlock()
				body := protocol.NewJSONObject(dingTalkField("accessToken", "private-token"))
				if expiry != nil {
					body.Set("expireIn", expiry)
				}
				_, _ = w.Write(bodyBytes(body))
			}))
			defer peer.Close()
			credentials := object(raw(map[string]any{"clientId": "id", "clientSecret": "secret"}))
			actions := []any{map[string]any{"kind": "token"}, map[string]any{"kind": "token"}}
			input := map[string]any{"base": peer.URL, "now": int64(1770000000123), "credentials": credentials, "actions": actions}
			source := runDingTalkSource(t, "dingtalk-api.fixture.ts", input)
			mu.Lock()
			sourceCount := count
			count = 0
			mu.Unlock()
			base, _ := url.Parse(peer.URL)
			api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: credentials, HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}, Now: func() time.Time { return time.UnixMilli(1770000000123) }})
			defer api.Close()
			values := []protocol.JSONObject{}
			for i := 0; i < 2; i++ {
				v, e := api.AccessToken(context.Background())
				values = append(values, wecomCaptured(v, e))
			}
			exactJSON(t, raw(values), source.Get("values"), "token output")
			mu.Lock()
			got := count
			mu.Unlock()
			if got != sourceCount {
				t.Fatalf("cache HTTP count %d want %d expiry %s", got, sourceCount, expiry)
			}
		})
	}
	for _, spec := range []struct {
		status int
		body   string
	}{{403, `{"message":"denied"}`}, {500, `{"message":4}`}, {200, `{"accessToken":0}`}, {200, `garbled`}, {200, `{"accessToken":true,"expireIn":0}`}} {
		t.Run(spec.body, func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(spec.status)
				_, _ = w.Write([]byte(spec.body))
			}))
			defer peer.Close()
			credentials := object(raw(map[string]any{"clientId": "id", "clientSecret": "secret"}))
			source := runDingTalkSource(t, "dingtalk-api.fixture.ts", map[string]any{"base": peer.URL, "now": 1770000000123, "credentials": credentials, "actions": []any{map[string]any{"kind": "token"}}})
			base, _ := url.Parse(peer.URL)
			api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: credentials, HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}})
			defer api.Close()
			v, e := api.AccessToken(context.Background())
			exactJSON(t, raw([]protocol.JSONObject{wecomCaptured(v, e)}), source.Get("values"), "actual token errors")
		})
	}
	// Every concurrent miss must reach the real HTTP boundary before either
	// returns. The original function intentionally has no singleflight.
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = w.Write([]byte(`{"accessToken":"private","expireIn":7200}`))
	}))
	defer peer.Close()
	base, _ := url.Parse(peer.URL)
	api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: object(raw(map[string]any{"clientId": "id", "clientSecret": "secret"})), HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}})
	defer api.Close()
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, e := api.AccessToken(context.Background()); done <- e }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("token misses collapsed")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
}
func bodyBytes(v protocol.JSONObject) []byte { return dingTalkWire(v) }
func TestDingTalkOriginalMediaDownloadExchangeMIMEAndPhysicalFiles(t *testing.T) {
	bytes := []byte{0, 255, 1, 2, 3, 0, 7}
	var mu sync.Mutex
	calls := []protocol.JSONObject{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, protocol.NewJSONObject(dingTalkField("method", r.Method), dingTalkField("path", r.URL.Path), dingTalkField("body", NewText(string(data))), dingTalkField("token", r.Header.Get("x-acs-dingtalk-access-token"))))
		mu.Unlock()
		switch r.URL.Path {
		case "/v1.0/robot/messageFiles/download":
			code := object(data).String("downloadCode")
			switch code {
			case "bad":
				w.WriteHeader(403)
				_, _ = w.Write([]byte(`{"message":"no exchange"}`))
			case "malformed":
				_, _ = w.Write([]byte(`{`))
			default:
				_, _ = w.Write([]byte(`{"downloadUrl":"https://private.invalid/file"}`))
			}
		case "/file":
			w.Header().Set("Content-Type", "image/png; private=preserved")
			_, _ = w.Write(bytes)
		case "/zstd":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Encoding", "zstd")
			encoder, _ := zstd.NewWriter(nil)
			encoded := encoder.EncodeAll(bytes, nil)
			encoder.Close()
			_, _ = w.Write(encoded)
		case "/infer":
			w.Header()["Content-Type"] = nil
			_, _ = w.Write(bytes)
		case "/fail":
			w.WriteHeader(404)
		}
	}))
	defer peer.Close()
	candidates := []protocol.JSONObject{}
	for _, s := range []string{`{"kind":"file","downloadCode":"good","fileName":"../a.PDF"}`, `{"kind":"image","url":"https://private.invalid/file"}`, `{"kind":"file","url":"https://private.invalid/infer","fileName":"text.TXT"}`, `{"kind":"image","url":"https://private.invalid/infer"}`, `{"kind":"file","url":"https://private.invalid/file","fileName":"a.PDF"}`, `{"kind":"file","downloadCode":"bad"}`, `{"kind":"file","downloadCode":"malformed"}`, `{"kind":"image"}`, `{"kind":"file","url":"https://private.invalid/fail"}`, `{"kind":"file","url":"https://private.invalid/zstd","fileName":"compressed.bin"}`} {
		candidates = append(candidates, object([]byte(s)))
	}
	options := object(raw(map[string]any{"clientId": true, "accessToken": "private-token"}))
	sourceStore := t.TempDir()
	source := runDingTalkSource(t, "dingtalk-media.fixture.ts", map[string]any{"base": peer.URL, "store": sourceStore, "options": options, "candidates": candidates})
	mu.Lock()
	wantCalls := append([]protocol.JSONObject{}, calls...)
	calls = nil
	mu.Unlock()
	store := NewAttachmentStore(t.TempDir())
	store.Now = func() time.Time { return time.UnixMilli(1770000000123) }
	store.Random = func() string { return dingTalkRandomSuffix(0.25, 6) }
	base, _ := url.Parse(peer.URL)
	api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: object(raw(map[string]any{"clientId": true})), HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}, Now: store.Now})
	defer api.Close()
	values := []protocol.JSONObject{}
	for _, candidate := range candidates {
		a, e := api.DownloadCandidate(context.Background(), store, candidate, "session", options.Get("accessToken"))
		if e != nil {
			values = append(values, protocol.NewJSONObject(dingTalkField("error", e.Error())))
			continue
		}
		disk, e := os.ReadFile(a.Path)
		if e != nil {
			t.Fatal(e)
		}
		path, e := filepath.Rel(store.Root, a.Path)
		if e != nil {
			t.Fatal(e)
		}
		values = append(values, protocol.NewJSONObject(dingTalkField("value", protocol.NewJSONObject(dingTalkField("kind", a.Kind), dingTalkField("name", a.Name), dingTalkField("path", path), dingTalkField("mime", a.MIME), dingTalkField("size", a.Size), dingTalkField("bytes", base64.StdEncoding.EncodeToString(a.Buffer)), dingTalkField("disk", base64.StdEncoding.EncodeToString(disk))))))
	}
	exactJSON(t, raw(values), source.Get("values"), "actual media bytes/MIME/files")
	mu.Lock()
	actual := append([]protocol.JSONObject{}, calls...)
	mu.Unlock()
	exactJSON(t, raw(actual), raw(wantCalls), "actual download/exchange wires")
	e := filepath.Walk(store.Root, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if strings.HasSuffix(path, ".part") {
			t.Errorf("escaped staged file %s", path)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
