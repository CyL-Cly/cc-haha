package imruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type qqMediaRoute struct{ peer *httptest.Server }

func (r qqMediaRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "cdn.qq.private" {
		return nil, fmt.Errorf("unowned QQ media logical URL")
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = "http"
	u.Host = r.peer.Listener.Addr().String()
	copy.URL = &u
	copy.Header.Set("X-Fixture-Logical-URL", req.URL.String())
	return r.peer.Client().Transport.RoundTrip(copy)
}
func TestQQOriginalMediaURLMIMEFilesAndPhysicalHTTP(t *testing.T) {
	rows := []map[string]any{}
	for _, a := range []map[string]any{{"url": "cdn.qq.private/image", "content_type": "image/png; charset=utf8"}, {"url": "http://cdn.qq.private/file", "filename": " ../notes.txt "}, {"url": "https://cdn.qq.private/x", "filename": "future.pdf"}, {"url": "https://cdn.qq.private/x", "content_type": "application/pdf"}, {"url": "file:///private-not-real"}, {"url": "https://u:p@cdn.qq.private/x"}} {
		rows = append(rows, map[string]any{"attachment": a, "data": "cHJpdmF0ZS1tZWRpYQ==", "mime": "image/jpeg; x=1"})
	}
	rows = append(rows, map[string]any{"attachment": map[string]any{"url": "cdn.qq.private/x"}, "data": "cHJpdmF0ZS1mYWlsdXJl", "status": 404})
	var expected []protocol.JSONObject
	_ = json.Unmarshal(qqOriginal(t, "native-qq-media.fixture.ts", map[string]any{"rows": rows}), &expected)
	home := t.TempDir()
	store := NewAttachmentStore(home)
	store.Root = filepath.Join(home, "private-downloads")
	var mu sync.Mutex
	var current map[string]any
	calls := []protocol.JSONObject{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		row := current
		calls = append(calls, qqObject(qqField("method", r.Method), qqField("url", r.Header.Get("X-Fixture-Logical-URL"))))
		mu.Unlock()
		mime, _ := row["mime"].(string)
		if mime == "" {
			mime = "application/octet-stream"
		}
		w.Header().Set("Content-Type", mime)
		status := 200
		if n, ok := row["status"]; ok {
			status = n.(int)
		}
		w.WriteHeader(status)
		data, _ := base64.StdEncoding.DecodeString(row["data"].(string))
		w.Write(data)
	}))
	defer peer.Close()
	media := QQMedia{store, &http.Client{Transport: qqMediaRoute{peer}}}
	for i, row := range rows {
		mu.Lock()
		current = row
		calls = []protocol.JSONObject{}
		mu.Unlock()
		v, e := media.Download(context.Background(), object(raw(row["attachment"])), "private-session", fmt.Sprint(i))
		var got protocol.JSONObject
		if e != nil {
			got = qqObject(qqField("error", e.Error()))
		} else {
			path, _ := filepath.Rel(store.Root, v.Path)
			data, e := os.ReadFile(v.Path)
			if e != nil {
				t.Fatal(e)
			}
			info, e := os.Stat(v.Path)
			if e != nil {
				t.Fatal(e)
			}
			got = qqObject(qqField("kind", v.Kind), qqField("name", v.Name), qqField("path", path), qqField("mime", v.MIME), qqField("size", v.Size), qqField("data", base64.StdEncoding.EncodeToString(data)), qqField("mode", info.Mode().Perm()))
		}
		mu.Lock()
		got.Set("calls", raw(calls))
		mu.Unlock()
		exactJSON(t, raw(got), raw(expected[i]), fmt.Sprint(i))
	}
}
