package imruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	whatwg "github.com/nlnwa/whatwg-url/url"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActualFeishuSourceOutboundLocalBase64PublicAddressAndPinnedRemoteRedirectMedia(t *testing.T) {
	home := t.TempDir()
	outside := filepath.Join(home, "outside-work.png")
	if e := os.WriteFile(outside, []byte("private-local-image"), 0600); e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{}
	for _, address := range []string{"8.8.8.8", "127.0.0.1", "10.1.2.3", "100.63.0.1", "100.64.0.1", "169.254.1.2", "172.15.0.1", "172.16.0.1", "192.0.2.1", "192.88.99.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.1.2.3", "2001:4860:4860::8888", "2001:db8::1", "2001:2::1", "2002::1", "3fff:fff::1", "3fff:1000::1", "::ffff:8.8.8.8", "::ffff:127.0.0.1", "fe80::1", "[2001:4860::1]"} {
		rows = append(rows, map[string]any{"kind": "address", "address": address})
	}
	rows = append(rows, map[string]any{"kind": "materialize", "source": UploadSource{Kind: "path", Path: outside}}, map[string]any{"kind": "materialize", "source": UploadSource{Kind: "base64", Data: base64.StdEncoding.EncodeToString([]byte("private-base64")), MIME: "image/jpeg"}})
	public := []FeishuRemoteAddress{{"8.8.8.8", 4}}
	private := []FeishuRemoteAddress{{"127.0.0.1", 4}}
	for _, profile := range []struct {
		url       string
		addresses []FeishuRemoteAddress
	}{{"file:///private/image", public}, {"http://user:secret@images.fixture/image", public}, {"invalid", public}, {"http://images.fixture/image", private}, {"http://images.fixture/image", []FeishuRemoteAddress{}}, {"http://images.fixture/image", public}, {"http://images.fixture/redirect", public}, {"http://images.fixture/private-redirect", public}, {"http://images.fixture/missing-location", public}, {"http://images.fixture/repeat", public}, {"http://images.fixture/mime", public}, {"http://images.fixture/encoding", public}, {"http://images.fixture/status", public}, {"http://images.fixture/size", public}} {
		rows = append(rows, map[string]any{"kind": "remote", "url": profile.url, "addresses": profile.addresses})
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", "/image")
			w.WriteHeader(302)
		case "/private-redirect":
			w.Header().Set("Location", "http://127.0.0.1/image")
			w.WriteHeader(302)
		case "/missing-location":
			w.WriteHeader(302)
		case "/repeat":
			w.Header().Set("Location", "/repeat")
			w.WriteHeader(307)
		case "/mime":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("not image"))
		case "/encoding":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Encoding", "private-invalid")
		case "/status":
			w.WriteHeader(404)
		case "/size":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", "10485761")
			_, _ = w.Write(make([]byte, 10485761))
		default:
			w.Header().Set("Content-Type", " image/png ; private")
			_, _ = w.Write([]byte("private-remote-image"))
		}
	}))
	defer peer.Close()
	want := runPlatformStageSource(t, "native-feishu-remote.fixture.ts", "CC_HAHA_FEISHU_REMOTE_FIXTURE", map[string]any{"rows": rows, "peer": peer.URL}, home)
	got := []any{}
	receipts := []any{}
	runtime := &FeishuRuntime{o: FeishuRuntimeOptions{}, ctx: context.Background()}
	for _, row := range rows {
		switch row["kind"] {
		case "address":
			got = append(got, FeishuPublicRemoteAddress(row["address"].(string)))
		case "materialize":
			image, e := runtime.materializeOutbound(row["source"].(UploadSource))
			if e != nil {
				got = append(got, map[string]any{"error": e.Error()})
			} else {
				got = append(got, map[string]any{"bytes": base64.StdEncoding.EncodeToString(image.Buffer), "mime": image.MIME})
			}
		case "remote":
			o := FeishuRemoteOptions{Resolve: func(ctx context.Context, host string) ([]FeishuRemoteAddress, error) {
				receipts = append(receipts, map[string]any{"resolve": host})
				return row["addresses"].([]FeishuRemoteAddress), nil
			}, Request: func(ctx context.Context, u *whatwg.Url, a FeishuRemoteAddress, _ time.Duration) (*http.Response, func(), error) {
				receipts = append(receipts, map[string]any{"url": u.Href(false), "address": a})
				target, _ := whatwg.ParseRef(peer.URL, u.Pathname()+u.Search())
				req, e := http.NewRequestWithContext(ctx, "GET", target.Href(false), nil)
				if e != nil {
					return nil, nil, e
				}
				client := peer.Client()
				client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
				response, e := client.Do(req)
				if e != nil {
					return nil, nil, e
				}
				return response, func() { _ = response.Body.Close() }, nil
			}}
			image := FeishuLoadRemoteImage(context.Background(), row["url"].(string), o)
			if image.OK {
				got = append(got, map[string]any{"ok": true, "bytes": base64.StdEncoding.EncodeToString(image.Buffer), "mime": image.MIME})
			} else {
				got = append(got, map[string]any{"ok": false, "reason": image.Reason})
			}
		}
	}
	exactJSON(t, raw(map[string]any{"results": got, "receipts": receipts}), want, "actual Source outbound image materialization and pinned hop consumers")
}

var _ = json.Valid
