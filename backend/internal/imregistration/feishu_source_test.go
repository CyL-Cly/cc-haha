package imregistration

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	whatwg "github.com/nlnwa/whatwg-url/url"
)

type feishuReply struct {
	Status int    `json:"status,omitempty"`
	Body   any    `json:"body,omitempty"`
	Raw    string `json:"raw,omitempty"`
	Hold   bool   `json:"hold,omitempty"`
}
type feishuAction struct {
	Kind string `json:"kind"`
	Now  *int64 `json:"now,omitempty"`
	Key  string `json:"key,omitempty"`
}
type feishuRow struct {
	Options map[string]any `json:"options,omitempty"`
	Actions []feishuAction `json:"actions"`
	Replies []feishuReply  `json:"replies"`
}

type feishuRedirect struct {
	destination *url.URL
	transport   http.RoundTripper
}

func (f feishuRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || (req.URL.Host != "accounts.feishu.cn" && req.URL.Host != "accounts.larksuite.com") || req.URL.Path != feishuRegistrationPath {
		return nil, io.ErrUnexpectedEOF
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = f.destination.Scheme
	u.Host = f.destination.Host
	clone.URL = &u
	clone.Header.Set("X-Fixture-Logical-URL", req.URL.String())
	return f.transport.RoundTrip(clone)
}
func feishuNormalize(t *testing.T, object protocol.JSONObject) map[string]any {
	t.Helper()
	raw, _ := object.MarshalJSON()
	var out map[string]any
	if e := json.Unmarshal(raw, &out); e != nil {
		t.Fatal(e)
	}
	if text, ok := out["verificationUri"].(string); ok {
		u, e := whatwg.Parse(text)
		if e != nil {
			t.Fatal(e)
		}
		compressed, e := base64.RawURLEncoding.DecodeString(feishuParams(u).Get("addons"))
		if e != nil {
			t.Fatal(e)
		}
		reader, e := gzip.NewReader(bytes.NewReader(compressed))
		if e != nil {
			t.Fatal(e)
		}
		p, e := io.ReadAll(reader)
		reader.Close()
		if e != nil {
			t.Fatal(e)
		}
		var addons any
		if e = json.Unmarshal(p, &addons); e != nil {
			t.Fatal(e)
		}

		out["addons"] = addons
	}
	return out
}
func TestFeishuActualSourceStateMachineAndPhysicalHTTP(t *testing.T) {
	begin := map[string]any{"device_code": " \ufeffprivate-device\ufeff ", "verification_uri_complete": "https://open.feishu.cn/page/launcher?from=old&x=1&from=again#frag", "expires_in": 600, "interval": 5}
	clone := func() map[string]any {
		r := map[string]any{}
		for k, v := range begin {
			r[k] = v
		}
		return r
	}
	actions := []feishuAction{{Kind: "begin"}, {Kind: "poll"}, {Kind: "poll"}}
	rows := []feishuRow{}
	for _, v := range []any{nil, 0, -1, "0x10", 1.5, true, false, "Infinity", []any{17}, []any{}, map[string]any{}} {
		b := clone()
		b["expires_in"] = v
		b["interval"] = v
		rows = append(rows, feishuRow{Actions: []feishuAction{{Kind: "begin"}}, Replies: []feishuReply{{Body: b}}})
	}
	for _, v := range []any{nil, false, 17, []any{}, "", " \ufeff "} {
		b := clone()
		b["device_code"] = v
		rows = append(rows, feishuRow{Actions: []feishuAction{{Kind: "begin"}}, Replies: []feishuReply{{Body: b}}})
	}
	for _, v := range []any{nil, false, 17, "not a url", "http://open.feishu.cn/x", "https://evil.example/x", "https://open.feishu.cn.evil.test/x", "https://u:p@open.feishu.cn/x", "https://open.feishu.cn:8443/x", "https://passport.feishu.cn/x", "https://OPEN.FEISHU.CN:443/x", "https://open.larksuite.com/x", "https://accounts.larksuite.com/x", "https://open.feishu.cn./x", "https://open.feishu.cn\\@evil.test/x"} {
		b := clone()
		b["verification_uri_complete"] = v
		rows = append(rows, feishuRow{Actions: []feishuAction{{Kind: "begin"}}, Replies: []feishuReply{{Body: b}}})
	}
	for _, query := range []string{"x=a%2Bb&x=old&name=old&name=again", "x=%ZZ&source=old", "x=%0g%aZ%a&source=old", "x=%F0%9F%98%80&x=%FF", "x=a+b&empty=&flag", "x=a%26b%3Dc"} {
		b := clone()
		b["verification_uri_complete"] = "https://open.feishu.cn/x?" + query
		rows = append(rows, feishuRow{Options: map[string]any{"source": "source + / * ~ & =", "appName": 17, "appDescription": []any{"a", "b"}}, Actions: []feishuAction{{Kind: "begin"}}, Replies: []feishuReply{{Body: b}}})
	}
	for _, v := range []any{"authorization_pending", "slow_down", "expired_token", "access_denied", "other", "", nil, false, 0, []any{}, map[string]any{}} {
		rows = append(rows, feishuRow{Actions: actions, Replies: []feishuReply{{Body: begin}, {Status: 400, Body: map[string]any{"error": v}}, {Body: map[string]any{"error": "authorization_pending"}}}})
	}
	rows = append(rows,
		feishuRow{Options: map[string]any{"domain": "lark", "source": "source + /", "appName": "Name", "appDescription": "描述"}, Actions: actions, Replies: []feishuReply{{Body: begin}, {Body: map[string]any{"client_id": "fake-id", "client_secret": "fake-secret", "user_info": map[string]any{"open_id": "ou-private"}}}}},
		feishuRow{Actions: actions, Replies: []feishuReply{{Body: begin}, {Body: map[string]any{"error": "authorization_pending", "user_info": map[string]any{"tenant_brand": "lark"}}}, {Status: 400, Body: map[string]any{"error": "slow_down", "user_info": map[string]any{"tenant_brand": "lark"}}}, {Body: map[string]any{"client_id": 17, "client_secret": true, "user_info": map[string]any{"open_id": nil, "tenant_brand": "lark"}}}}},
		feishuRow{Actions: actions, Replies: []feishuReply{{Body: begin}, {Raw: "not-json"}, {Body: map[string]any{}}}},
		feishuRow{Actions: actions, Replies: []feishuReply{{Body: begin}, {Body: map[string]any{"user_info": map[string]any{"tenant_brand": "lark"}}}, {Raw: "null"}, {Body: map[string]any{}}}},
		feishuRow{Actions: []feishuAction{{Kind: "begin"}, {Kind: "cancel"}, {Kind: "poll"}}, Replies: []feishuReply{{Body: begin}}},
		feishuRow{Actions: []feishuAction{{Kind: "begin"}, {Kind: "cancel_poll"}, {Kind: "poll"}}, Replies: []feishuReply{{Body: begin}, {Hold: true, Body: map[string]any{"client_id": "late-id", "client_secret": "late-secret"}}}},
		feishuRow{Actions: []feishuAction{{Kind: "poll"}}, Replies: nil},
	)
	for _, n := range []int64{601000, 601001, 1801000, 1801001} {
		b := clone()
		if n >= 1801000 {
			b["expires_in"] = 3600
		}
		rows = append(rows, feishuRow{Actions: []feishuAction{{Kind: "begin"}, {Kind: "poll", Now: &n}}, Replies: []feishuReply{{Body: b}, {Body: map[string]any{}}}})
	}
	for _, raw := range []string{"null", "false", "0", "\"\"", "[]", "17", "true", "not-json"} {
		rows = append(rows, feishuRow{Actions: []feishuAction{{Kind: "begin"}}, Replies: []feishuReply{{Status: 400, Raw: raw}}})
	}
	rows = append(rows, feishuRow{Actions: []feishuAction{{Kind: "begin"}}, Replies: []feishuReply{{Body: map[string]any{"error": map[string]any{"x": 17}, "error_description": []any{"reason", "other"}}}}})
	input, _ := json.Marshal(map[string]any{"rows": rows})
	expected := runFeishuSource(t, input)
	if len(expected) != len(rows) {
		t.Fatal("Source row count", len(expected), len(rows))
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var mu sync.Mutex
			requests := []any{}
			at := 0
			now := int64(1000)
			pending := make(chan struct{}, 1)
			release := make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p, e := io.ReadAll(r.Body)
				if e != nil {
					t.Error(e)
				}
				mu.Lock()
				requests = append(requests, map[string]any{"url": r.Header.Get("X-Fixture-Logical-URL"), "method": r.Method, "contentType": r.Header.Get("Content-Type"), "body": string(p)})
				if at >= len(row.Replies) {
					mu.Unlock()
					t.Error("unexpected HTTP")
					w.WriteHeader(500)
					return
				}
				reply := row.Replies[at]
				at++
				mu.Unlock()
				if reply.Hold {
					pending <- struct{}{}
					<-release
				}
				status := reply.Status
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				if reply.Raw != "" {
					io.WriteString(w, reply.Raw)
				} else {
					json.NewEncoder(w).Encode(reply.Body)
				}
			}))
			defer peer.Close()

			destination, _ := url.Parse(peer.URL)
			client := &http.Client{Transport: feishuRedirect{destination, peer.Client().Transport}}
			s, e := NewFeishu(FeishuOptions{Client: client, Now: func() time.Time { return time.UnixMilli(now) }, UUID: func() string { return "private-fixed-key" }})
			if e != nil {
				t.Fatal(e)
			}
			optionsRaw, _ := json.Marshal(row.Options)
			var options protocol.JSONObject
			if row.Options != nil {
				_ = options.UnmarshalJSON(optionsRaw)
			}
			key := encoded("private-fixed-key")
			if truthy(options.Get("sessionKey")) {
				key = options.Get("sessionKey")
			}
			options.Set("sessionKey", key)
			values := []any{}
			for _, action := range row.Actions {
				if action.Now != nil {
					now = *action.Now
				}
				var result protocol.JSONObject
				var err error
				switch action.Kind {
				case "begin":
					result, err = s.Begin(context.Background(), options)
				case "cancel":
					err = s.CancelJSON(key)
					result = record(field("cancelled", true))
				case "cancel_poll":
					type outcome struct {
						value protocol.JSONObject
						err   error
					}
					done := make(chan outcome, 1)
					go func() { v, e := s.PollJSON(context.Background(), key); done <- outcome{v, e} }()
					select {
					case <-pending:
					case <-time.After(3 * time.Second):
						t.Fatal("physical poll not started")
					}
					if e := s.CancelJSON(key); e != nil {
						t.Fatal(e)
					}
					close(release)
					o := <-done
					result, err = o.value, o.err
				default:
					actualKey := key
					if action.Key != "" {
						actualKey = encoded(action.Key)
					}
					result, err = s.PollJSON(context.Background(), actualKey)
				}
				if err == nil && action.Kind == "begin" && row.Options != nil && row.Options["source"] != nil {
					u, e := url.Parse(result.String("verificationUri"))
					if e != nil || u.Query().Get("source") != row.Options["source"] {
						t.Fatalf("raw produced URL source corrupted: %s %v", result.String("verificationUri"), e)
					}
				}
				if err != nil {
					values = append(values, map[string]any{"error": err.Error()})
				} else {
					values = append(values, map[string]any{"value": feishuNormalize(t, result)})
				}
			}
			mu.Lock()
			got := map[string]any{"values": values, "requests": requests, "raw": map[string]any{}, "mode": float64(0600)}
			mu.Unlock()
			if !reflect.DeepEqual(got, expected[i]) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(expected[i])
				t.Fatalf("actual Source row%d\nGo %s\nSource %s", i, a, b)
			}
		})
	}
	t.Logf("%d actual Source scenarios, genuine local HTTP requests", len(rows))
}
func runFeishuSource(t *testing.T, input []byte) []map[string]any {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	private := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture, e := filepath.Abs("../../../scripts/quality-gate/go-parity/native-feishu-registration.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = private
	cmd.Env = []string{"HOME=" + private, "USERPROFILE=" + private, "CLAUDE_CONFIG_DIR=" + filepath.Join(private, ".claude"), "TMPDIR=" + private, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(private), "CC_HAHA_FEISHU_REGISTRATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source", e, stderr.String())
	}
	var result []map[string]any
	if e = json.Unmarshal(out, &result); e != nil {
		t.Fatal(e, string(out), stderr.String())
	}
	return result
}

func TestFeishuRequestTimeoutIsPhysicalAndBodyReadSharesDeadline(t *testing.T) {
	for _, holdBody := range []bool{false, true} {
		t.Run(fmt.Sprint(holdBody), func(t *testing.T) {
			observed := make(chan struct{}, 1)
			released := make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				observed <- struct{}{}
				if holdBody {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"device_code":`)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-released:
				}
			}))
			defer peer.Close()
			defer close(released)
			destination, _ := url.Parse(peer.URL)
			client := &http.Client{Transport: feishuDeadlineTransport{base: feishuRedirect{destination, peer.Client().Transport}}}
			service, e := NewFeishu(FeishuOptions{Client: client})
			if e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { _, e := service.Begin(ctx, record()); done <- e }()
			select {
			case <-observed:
			case <-time.After(3 * time.Second):
				t.Fatal("actual private HTTP did not begin")
			}
			cancel()
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("cancelled HTTP produced success")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("owned HTTP/body did not close")
			}
		})
	}
}

type feishuDeadlineTransport struct{ base http.RoundTripper }

func (f feishuDeadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	deadline, ok := r.Context().Deadline()
	if !ok || time.Until(deadline) > FeishuRequestTimeout || time.Until(deadline) < 14*time.Second {
		return nil, fmt.Errorf("original 15-second request deadline missing")
	}
	if !strings.HasPrefix(r.URL.String(), "https://accounts.") {
		return nil, fmt.Errorf("unexpected URL")
	}
	return f.base.RoundTrip(r)
}
