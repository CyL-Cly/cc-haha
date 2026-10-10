package imregistration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type wechatReply struct {
	Status     int    `json:"status,omitempty"`
	Body       any    `json:"body,omitempty"`
	Raw        string `json:"raw,omitempty"`
	Disconnect bool   `json:"disconnect,omitempty"`
	Redirect   bool   `json:"redirect,omitempty"`
}
type wechatAction struct {
	Kind    string          `json:"kind"`
	Now     *int64          `json:"now,omitempty"`
	Options map[string]any  `json:"options,omitempty"`
	Key     json.RawMessage `json:"key,omitempty"`
}
type wechatRow struct {
	Options map[string]any `json:"options,omitempty"`
	Actions []wechatAction `json:"actions"`
	Replies []wechatReply  `json:"replies"`
}
type wechatRedirect struct {
	destination *url.URL
	transport   http.RoundTripper
	observe     func(*http.Request)
}

func (f wechatRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || (!strings.HasSuffix(req.URL.Host, ".test") && req.URL.Host != "ilinkai.weixin.qq.com") {
		return nil, fmt.Errorf("unowned WeChat logical target")
	}
	if f.observe != nil && req.URL.Path != "/private-follow" {
		f.observe(req)
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = f.destination.Scheme
	u.Host = f.destination.Host
	clone.URL = &u
	return f.transport.RoundTrip(clone)
}
func TestWechatActualSourceStateMachineHeadersHTTPJSONCacheTTLAndRawUTF16(t *testing.T) {
	qr := map[string]any{"qrcode": "private QR + / ~ ! ' ( ) 汉😀", "qrcode_img_content": "https://private.test/qr"}
	confirmed := map[string]any{"status": "confirmed", "bot_token": "private-token", "ilink_bot_id": "private-id", "ilink_user_id": "private-user"}
	rows := []wechatRow{}
	rows = append(rows, wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "poll"}}, Replies: []wechatReply{{Redirect: true}, {Body: qr}, {Redirect: true}, {Body: confirmed}}})
	for _, botType := range []any{nil, "", false, 0, 3, "3", json.RawMessage(`"\ud800"`), " + / ~ ! ' ( ) 汉😀", []any{1, 2}, map[string]any{}} {
		rows = append(rows, wechatRow{Options: map[string]any{"botType": botType}, Actions: []wechatAction{{Kind: "start"}}, Replies: []wechatReply{{Body: qr}}})
	}
	for _, status := range []any{"wait", "scaned", "scaned_but_redirect", "expired", "confirmed", "unknown", "SCANED", nil, 17, false, []any{}, map[string]any{}} {
		rows = append(rows, wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}}, Replies: []wechatReply{{Body: qr}, {Body: map[string]any{"status": status}}, {Body: confirmed}}})
	}
	for _, value := range []any{nil, false, 0, "", 17, true, []any{}, map[string]any{}} {
		rows = append(rows, wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}}, Replies: []wechatReply{{Body: qr}, {Body: map[string]any{"status": "confirmed", "bot_token": value, "ilink_bot_id": "private-id"}}, {Body: confirmed}}})
	}
	n1, n2 := int64(300999), int64(301000)
	rows = append(rows,
		wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "start"}}, Replies: []wechatReply{{Body: qr}}},
		wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "start", Options: map[string]any{"force": true}}}, Replies: []wechatReply{{Body: qr}, {Body: qr}}},
		wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "start", Now: &n1}}, Replies: []wechatReply{{Body: qr}}},
		wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "start", Now: &n2}}, Replies: []wechatReply{{Body: qr}, {Body: qr}}},
		wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "poll", Now: &n1}}, Replies: []wechatReply{{Body: qr}, {Body: confirmed}}},
		wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "poll", Now: &n2}}, Replies: []wechatReply{{Body: qr}}},
		wechatRow{Actions: []wechatAction{{Kind: "poll"}}},
		wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}}, Replies: []wechatReply{{Body: qr}, {Body: map[string]any{"status": "scaned_but_redirect", "redirect_host": "REDIRECT.test/gateway"}}, {Body: confirmed}}},
		wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}}, Replies: []wechatReply{{Body: qr}, {Body: map[string]any{"status": "scaned_but_redirect", "redirect_host": "not valid % host"}}, {Body: confirmed}}},
		wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "poll"}}, Replies: []wechatReply{{Body: qr}, {Raw: `{"status":"confirmed","bot_token":"\ud800","ilink_bot_id":"\ud801","baseurl":"https://private.test/next","ilink_user_id":"\ud802"}`}}},
		wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "poll"}}, Replies: []wechatReply{{Raw: `{"qrcode":"\ud800","qrcode_img_content":"\ud801"}`}}},
		wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "poll", Key: json.RawMessage(`"\ud800"`)}}, Options: map[string]any{"sessionKey": json.RawMessage(`"\ud800"`)}, Replies: []wechatReply{{Body: qr}, {Body: confirmed}}},
	)
	for _, r := range []wechatReply{{Status: 400, Raw: "private failure"}, {Status: 500, Body: qr}, {Raw: "not-json"}, {Raw: "null"}, {Raw: "[]"}, {Raw: "false"}, {Raw: "17"}, {Raw: "\ufeff{\"qrcode\":\"s\",\"qrcode_img_content\":\"u\"}"}, {Body: map[string]any{"qrcode": "s"}}, {Body: map[string]any{"qrcode": 17, "qrcode_img_content": true}}} {
		rows = append(rows, wechatRow{Actions: []wechatAction{{Kind: "start"}}, Replies: []wechatReply{r}})
	}
	for _, r := range []wechatReply{{Status: 400, Raw: "private failure"}, {Status: 500, Body: qr}, {Raw: "not-json"}, {Raw: "null"}, {Raw: "[]"}, {Raw: "false"}, {Raw: "17"}, {Disconnect: true}} {
		rows = append(rows, wechatRow{Actions: []wechatAction{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}}, Replies: []wechatReply{{Body: qr}, r, {Body: confirmed}}})
	}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	expected := runWechatSource(t, input)
	if len(expected) != len(rows) {
		t.Fatal("Source row count")
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var mu sync.Mutex
			at := 0
			requests := []any{}
			now := int64(1000)
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("ilink-app-id") != "bot" || r.Header.Get("ilink-app-clientversion") != WechatClientVersion {
					t.Error("actual initial/redirect physical headers lost")
				}
				mu.Lock()
				if at >= len(row.Replies) {
					mu.Unlock()
					t.Error("unexpected physical request")
					w.WriteHeader(500)
					return
				}
				reply := row.Replies[at]
				at++
				mu.Unlock()
				if reply.Disconnect {
					c, _, _ := w.(http.Hijacker).Hijack()
					c.Close()
					return
				}
				status := reply.Status
				if status == 0 {
					status = 200
					if reply.Redirect {
						status = 302
					}
				}
				if reply.Redirect {
					w.Header().Set("Location", "/private-follow")
				}
				w.WriteHeader(status)
				if reply.Raw != "" {
					io.WriteString(w, reply.Raw)
				} else {
					raw, _ := json.Marshal(reply.Body)
					_, _ = w.Write(raw)
				}
			}))
			defer peer.Close()
			target, _ := url.Parse(peer.URL)
			service, e := NewWechat(WechatOptions{Client: &http.Client{Transport: wechatRedirect{target, peer.Client().Transport, func(req *http.Request) {
				_, timed := req.Context().Deadline()
				mu.Lock()
				requests = append(requests, map[string]any{"url": req.URL.String(), "method": req.Method, "appId": req.Header.Get("ilink-app-id"), "clientVersion": req.Header.Get("ilink-app-clientversion"), "timed": timed})
				mu.Unlock()
			}}}, Now: func() time.Time { return time.UnixMilli(now) }, UUID: func() string { return "private-fixed-key" }})
			if e != nil {
				t.Fatal(e)
			}
			defer service.Close(context.Background())
			opts := record()
			for k, v := range row.Options {
				opts.Set(k, encoded(v))
			}
			key := opts.Get("sessionKey")
			if len(key) == 0 {
				key = encoded("private-fixed-key")
			}
			opts.Set("sessionKey", key)
			values := []any{}
			for _, a := range row.Actions {
				if a.Now != nil {
					now = *a.Now
				}
				var value protocol.JSONObject
				var e error
				if a.Kind == "start" {
					o := opts.Clone()
					for k, v := range a.Options {
						o.Set(k, encoded(v))
					}
					value, e = service.Start(context.Background(), o)
				} else {
					selected := key
					if len(a.Key) > 0 {
						selected = a.Key
					}
					value, e = service.PollJSON(context.Background(), selected)
				}
				if e != nil {
					message := e.Error()
					if message == "invalid JSON" {
						message = "<invalid JSON>"
					}
					values = append(values, map[string]any{"error": message})
				} else if !value.IsObject() {
					values = append(values, map[string]any{})
				} else {
					values = append(values, map[string]any{"value": asNativeJSON(t, value)})
				}
			}
			mu.Lock()
			got := map[string]any{"values": values, "requests": requests, "wireCalls": float64(at), "raw": map[string]any{}, "mode": float64(0600)}
			mu.Unlock()
			if !reflect.DeepEqual(got, expected[i]) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(expected[i])
				t.Fatalf("actual Source row%d\nGo%s\nSource%s", i, a, b)
			}
		})
	}
	t.Logf("%d original Source profiles", len(rows))
}
func runWechatSource(t *testing.T, input []byte) []map[string]any {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	root := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-wechat-registration.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, ".claude"), "TMPDIR=" + root, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(root), "CC_HAHA_WECHAT_REGISTRATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var result []map[string]any
	if e = json.Unmarshal(out, &result); e != nil {
		t.Fatal(e, string(out))
	}
	return result
}

func asNativeJSON(t *testing.T, value protocol.JSONObject) any {
	t.Helper()
	raw, e := value.MarshalJSON()
	if e != nil {
		t.Fatal(e)
	}
	var result any
	if e = json.Unmarshal(raw, &result); e != nil {
		t.Fatal(e)
	}
	return result
}
