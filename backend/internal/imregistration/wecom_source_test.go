package imregistration

import (
	"bytes"
	"context"
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
)

type wecomReply struct {
	Status   int    `json:"status,omitempty"`
	Body     any    `json:"body,omitempty"`
	Raw      string `json:"raw,omitempty"`
	Hold     bool   `json:"hold,omitempty"`
	Redirect bool   `json:"redirect,omitempty"`
}
type wecomAction struct {
	Kind    string         `json:"kind"`
	Now     *int64         `json:"now,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}
type wecomRow struct {
	Platform string         `json:"platform,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
	Actions  []wecomAction  `json:"actions"`
	Replies  []wecomReply   `json:"replies"`
}
type wecomRedirect struct {
	destination *url.URL
	transport   http.RoundTripper
}

func (f wecomRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "work.weixin.qq.com" || (req.URL.Path != "/ai/qc/generate" && req.URL.Path != "/ai/qc/query_result") {
		return nil, fmt.Errorf("unowned WeCom logical target")
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = f.destination.Scheme
	u.Host = f.destination.Host
	clone.URL = &u
	clone.Header.Set("X-Fixture-Logical-URL", req.URL.String())
	return f.transport.RoundTrip(clone)
}
func TestWecomActualSourceStateMachineAndPhysicalHTTP(t *testing.T) {
	generated := map[string]any{"data": map[string]any{"scode": " \ufeffprivate-scode\ufeff ", "auth_url": " https://work.weixin.qq.com/ai/qc/confirm?scode=private#frag "}}
	rows := []wecomRow{}
	start := []wecomAction{{Kind: "start"}}
	for _, platform := range []string{"darwin", "win32", "linux", "freebsd"} {
		rows = append(rows, wecomRow{Platform: platform, Actions: start, Replies: []wecomReply{{Body: generated}}})
	}
	for _, source := range []any{nil, "", false, 17, []any{"a", "b"}, "Source + / * ~ & =", map[string]any{}} {
		rows = append(rows, wecomRow{Options: map[string]any{"source": source}, Actions: start, Replies: []wecomReply{{Body: generated}}})
	}
	for _, value := range []any{nil, false, 17, []any{}, map[string]any{}, "", " \ufeff "} {
		rows = append(rows, wecomRow{Actions: start, Replies: []wecomReply{{Body: map[string]any{"data": map[string]any{"scode": value, "auth_url": "https://work.weixin.qq.com/x"}}}}})
	}
	for _, value := range []any{nil, false, 17, []any{}, "", "not a URL", "http://work.weixin.qq.com/x", "https://evil.test/x", "https://u:p@work.weixin.qq.com/x", "https://work.weixin.qq.com:8443/x", "https://WORK.WEIXIN.QQ.COM:443/x", "https://work.weixin.qq.com./x", "https://work.weixin.qq.com\\@evil.test/x"} {
		rows = append(rows, wecomRow{Actions: start, Replies: []wecomReply{{Body: map[string]any{"data": map[string]any{"scode": "s", "auth_url": value}}}}})
	}
	for _, status := range []any{nil, false, 17, []any{"success"}, map[string]any{}, "", "pending", "scanned", " SUCCESS ", "expired", "TIMEOUT", " fail ", "failed", "ERROR", "authorization_pending", "İ", "ß"} {
		rows = append(rows, wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}}, Replies: []wecomReply{{Body: generated}, {Body: map[string]any{"data": map[string]any{"status": status, "bot_info": map[string]any{"botid": " bot-private ", "secret": " secret-private "}}}}, {Body: map[string]any{}}}})
	}
	for _, bad := range []any{nil, false, 17, []any{}, "", " \ufeff "} {
		rows = append(rows, wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}}, Replies: []wecomReply{{Body: generated}, {Body: map[string]any{"data": map[string]any{"status": "success", "bot_info": map[string]any{"botid": bad, "secret": "s"}}}}}})
	}
	n1, n2 := int64(300999), int64(301000)
	rows = append(rows,
		wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "start"}}, Replies: []wecomReply{{Body: generated}}},
		wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "start", Options: map[string]any{"force": true}}}, Replies: []wecomReply{{Body: generated}, {Body: generated}}},
		wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "start", Now: &n1}}, Replies: []wecomReply{{Body: generated}}},
		wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "start", Now: &n2}}, Replies: []wecomReply{{Body: generated}, {Body: generated}}},
		wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "poll", Now: &n1}}, Replies: []wecomReply{{Body: generated}, {Body: map[string]any{}}}},
		wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "poll", Now: &n2}}, Replies: []wecomReply{{Body: generated}}},
		wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "cancel"}, {Kind: "poll"}}, Replies: []wecomReply{{Body: generated}}},
		wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "cancel_poll"}, {Kind: "poll"}}, Replies: []wecomReply{{Body: generated}, {Hold: true, Body: map[string]any{"data": map[string]any{"status": "success", "bot_info": map[string]any{"botid": "late", "secret": "late"}}}}}},
		wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}}, Replies: []wecomReply{{Body: generated}, {Raw: "null"}, {Body: map[string]any{}}}},
		wecomRow{Actions: []wecomAction{{Kind: "poll"}}},
	)
	rows = append(rows, wecomRow{Actions: []wecomAction{{Kind: "start"}, {Kind: "poll"}}, Replies: []wecomReply{{Body: generated}, {Raw: `{"data":{"status":"success","bot_info":{"botid":" \ud800 ","secret":" \ud801 "}}}`}}})
	for _, reply := range []wecomReply{{Status: 400, Body: generated}, {Status: 500, Body: generated}, {Status: 302, Redirect: true}, {Raw: "null"}, {Raw: "[]"}, {Raw: "false"}, {Raw: "17"}, {Raw: "not-json"}, {Raw: "\ufeff{\"data\":{\"scode\":\"s\",\"auth_url\":\"https://work.weixin.qq.com/x\"}}"}} {
		rows = append(rows, wecomRow{Actions: start, Replies: []wecomReply{reply}})
	}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	expected := runWecomSource(t, input)
	if len(expected) != len(rows) {
		t.Fatal("Source row count")
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
				mu.Lock()
				requests = append(requests, map[string]any{"url": r.Header.Get("X-Fixture-Logical-URL"), "method": r.Method, "accept": r.Header.Get("Accept")})
				if at >= len(row.Replies) {
					mu.Unlock()
					t.Error("unexpected physical request")
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
				if reply.Redirect {
					w.Header().Set("Location", "http://"+r.Host+"/redirect-target")
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
			client := &http.Client{Transport: wecomRedirect{destination, peer.Client().Transport}}
			service, e := NewWecom(WecomOptions{Client: client, Platform: row.Platform, Now: func() time.Time { return time.UnixMilli(now) }, UUID: func() string { return "private-fixed-key" }})
			if e != nil {
				t.Fatal(e)
			}
			var opts protocol.JSONObject
			p, _ := json.Marshal(row.Options)
			if row.Options != nil {
				_ = opts.UnmarshalJSON(p)
			}
			key := encoded("private-fixed-key")
			opts.Set("sessionKey", key)
			values := []any{}
			for _, action := range row.Actions {
				if action.Now != nil {
					now = *action.Now
				}
				var value protocol.JSONObject
				var err error
				switch action.Kind {
				case "start":
					actual := opts.Clone()
					if action.Options != nil {
						var extra protocol.JSONObject
						p, _ := json.Marshal(action.Options)
						_ = extra.UnmarshalJSON(p)
						actual.Assign(extra)
					}
					value, err = service.Start(context.Background(), actual)
				case "cancel":
					err = service.CancelJSON(key)
					value = record(field("cancelled", true))
				case "cancel_poll":
					type result struct {
						v protocol.JSONObject
						e error
					}
					done := make(chan result, 1)
					go func() { v, e := service.PollJSON(context.Background(), key); done <- result{v, e} }()
					select {
					case <-pending:
					case <-time.After(3 * time.Second):
						t.Fatal("physical pending poll absent")
					}
					if e := service.CancelJSON(key); e != nil {
						t.Fatal(e)
					}
					close(release)
					r := <-done
					value, err = r.v, r.e
				default:
					value, err = service.PollJSON(context.Background(), key)
				}
				if err != nil {
					message := err.Error()
					if strings.Contains(strings.ToLower(message), "redirect") {
						message = "<redirect rejected>"
					}
					values = append(values, map[string]any{"error": message})
				} else {
					p, _ := value.MarshalJSON()
					var v any
					_ = json.Unmarshal(p, &v)
					values = append(values, map[string]any{"value": v})
				}
			}
			mu.Lock()
			got := map[string]any{"values": values, "requests": requests, "raw": map[string]any{}, "mode": float64(0600)}
			mu.Unlock()
			if !reflect.DeepEqual(got, expected[i]) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(expected[i])
				t.Fatalf("Source row%d\nGo%s\nSource%s", i, a, b)
			}
		})
	}
	t.Logf("%d actual Source state/URL/HTTP/cache/cancel profiles", len(rows))
}
func runWecomSource(t *testing.T, input []byte) []map[string]any {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	private := t.TempDir()
	fixture, e := filepath.Abs("../../../scripts/quality-gate/go-parity/native-wecom-registration.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = private
	cmd.Env = []string{"HOME=" + private, "USERPROFILE=" + private, "CLAUDE_CONFIG_DIR=" + filepath.Join(private, ".claude"), "TMPDIR=" + private, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(private), "CC_HAHA_WECOM_REGISTRATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
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

func TestWecomRequestHasOriginalDeadlineAndCancellationClosesBody(t *testing.T) {
	for _, body := range []bool{false, true} {
		t.Run(fmt.Sprint(body), func(t *testing.T) {
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				started <- struct{}{}
				if body {
					io.WriteString(w, `{"data":`)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer peer.Close()
			defer close(release)
			destination, _ := url.Parse(peer.URL)
			service, e := NewWecom(WecomOptions{Client: &http.Client{Transport: wecomDeadline{wecomRedirect{destination, peer.Client().Transport}}}})
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, e := service.Start(ctx, record()); done <- e }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("physical request not started")
			}
			cancel()
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("cancel produced success")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("request/body did not close")
			}
		})
	}
}

type wecomDeadline struct{ base http.RoundTripper }

func (f wecomDeadline) RoundTrip(req *http.Request) (*http.Response, error) {
	deadline, ok := req.Context().Deadline()
	if !ok || time.Until(deadline) > WecomRequestTimeout || time.Until(deadline) < 9*time.Second {
		return nil, fmt.Errorf("original 10-second deadline missing")
	}
	return f.base.RoundTrip(req)
}
