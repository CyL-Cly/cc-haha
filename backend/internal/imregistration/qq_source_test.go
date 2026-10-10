package imregistration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type qqReply struct {
	Status     int     `json:"status,omitempty"`
	Body       any     `json:"body,omitempty"`
	Raw        string  `json:"raw,omitempty"`
	Hold       bool    `json:"hold,omitempty"`
	Redirect   bool    `json:"redirect,omitempty"`
	Secret     *string `json:"secret,omitempty"`
	AppID      any     `json:"appId,omitempty"`
	UserOpenID any     `json:"userOpenid,omitempty"`
}
type qqAction struct {
	Kind     string         `json:"kind"`
	Target   string         `json:"target,omitempty"`
	Index    int            `json:"index,omitempty"`
	Count    int            `json:"count,omitempty"`
	Duration int            `json:"duration,omitempty"`
	Now      *int64         `json:"now,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
}
type qqRow struct {
	Options map[string]any `json:"options,omitempty"`
	Actions []qqAction     `json:"actions"`
	Replies []qqReply      `json:"replies"`
}
type qqTimer struct {
	d time.Duration
	f func()
}
type qqClock struct {
	mu     sync.Mutex
	now    int64
	at     int
	timers map[int]qqTimer
}

func newQQClock() *qqClock        { return &qqClock{now: 1000, timers: map[int]qqTimer{}} }
func (c *qqClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return time.UnixMilli(c.now) }
func (c *qqClock) Schedule(d time.Duration, f func()) func() {
	c.mu.Lock()
	c.at++
	id := c.at
	c.timers[id] = qqTimer{d, f}
	c.mu.Unlock()
	return func() { c.mu.Lock(); delete(c.timers, id); c.mu.Unlock() }
}
func (c *qqClock) has(d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range c.timers {
		if v.d == d {
			return true
		}
	}
	return false
}
func (c *qqClock) fire(t *testing.T, d time.Duration) {
	t.Helper()
	c.mu.Lock()
	id := 0
	var f func()
	for k, v := range c.timers {
		if v.d == d && (id == 0 || k < id) {
			id = k
			f = v.f
		}
	}
	if id != 0 {
		delete(c.timers, id)
	}
	c.mu.Unlock()
	if f == nil {
		t.Fatalf("missing actual driver timer %s", d)
	}
	f()
}
func qqWait(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("QQ actual consumer barrier timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
func qqValue(v protocol.JSONObject, e error) map[string]any {
	if e != nil {
		return map[string]any{"error": e.Error()}
	}
	b, _ := v.MarshalJSON()
	var out any
	_ = json.Unmarshal(b, &out)
	return map[string]any{"value": out}
}
func qqCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	cert, e := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	priv, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv}))
}
func qqFixtureClient(peer *httptest.Server) *http.Client {
	tr := peer.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = nil
	address := strings.TrimPrefix(peer.URL, "https://")
	tr.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != "q.qq.com:443" {
			return nil, fmt.Errorf("unowned QQ target %s", target)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // generated isolated loopback certificate only
	return &http.Client{Transport: tr}
}

func TestQQActualSourceFacadeAndVendorHTTPSDriver(t *testing.T) {
	create := func(id any) qqReply {
		return qqReply{Body: map[string]any{"retcode": 0, "data": map[string]any{"task_id": id}}}
	}
	status := func(n any) qqReply {
		return qqReply{Body: map[string]any{"retcode": 0, "data": map[string]any{"status": n}}}
	}
	secret, empty := "private-secret", ""
	start := qqAction{Kind: "start"}
	waitSleep := qqAction{Kind: "wait", Target: "sleep"}
	poll := qqAction{Kind: "poll"}
	tick := qqAction{Kind: "tick"}
	waitSuccess := qqAction{Kind: "wait", Target: "success"}
	waitFailed := qqAction{Kind: "wait", Target: "failed"}
	rows := []qqRow{
		{Actions: []qqAction{poll}},
		{Actions: []qqAction{start, waitSleep, poll, {Kind: "cancel"}, poll}, Replies: []qqReply{create("private-task"), status(1)}},
		{Actions: []qqAction{start, waitSuccess, poll, poll}, Replies: []qqReply{create("private-task"), {Secret: &secret, UserOpenID: "private-open-id"}}},
		{Options: map[string]any{"source": "a b!*中国😀", "sessionKey": "private-key😀"}, Actions: []qqAction{start, waitSuccess, poll, poll}, Replies: []qqReply{create(17), {Secret: &secret, AppID: 42}}},
		{Actions: []qqAction{start, waitSleep, poll, tick, waitSuccess, poll, poll}, Replies: []qqReply{create("private-task"), {Status: 500, Body: map[string]any{"retcode": 0}}, {Secret: &secret}}},
		{Actions: []qqAction{start, {Kind: "wait", Target: "qr", Count: 2}, waitSleep, poll, poll, tick, waitSuccess, poll}, Replies: []qqReply{create("first-task"), status(3), create("second-task"), status(1), {Secret: &secret}}},
		{Actions: []qqAction{start, {Kind: "wait", Target: "terminal"}, poll, poll}, Replies: []qqReply{create("private-task"), {Secret: &empty}}},
		{Actions: []qqAction{start, {Kind: "wait", Target: "terminal"}, poll, poll}, Replies: []qqReply{create("private-task"), {Secret: &secret, AppID: ""}}},
		{Actions: []qqAction{start, waitFailed, poll, poll}, Replies: []qqReply{create("private-task"), {Body: map[string]any{"retcode": 0, "data": map[string]any{"status": 2, "bot_appid": "private-app", "bot_encrypt_secret": "bad"}}}}},
		{Actions: []qqAction{start, {Kind: "wait", Target: "request", Index: 1}, {Kind: "cancel"}, poll, {Kind: "release", Index: 1}, waitSuccess, poll}, Replies: []qqReply{create("private-task"), {Secret: &secret, Hold: true}}},
		{Actions: []qqAction{{Kind: "start_async"}, {Kind: "wait", Target: "request"}, {Kind: "tick", Duration: 20000}, {Kind: "await_start"}, poll, {Kind: "release"}, waitFailed, poll}, Replies: []qqReply{{Body: create("late-task").Body, Hold: true}}},
	}
	rows = append(rows,
		qqRow{Actions: []qqAction{start, waitSleep, start, waitSleep, poll, {Kind: "cancel"}, poll}, Replies: []qqReply{create("first"), status(1), create("second"), status(1)}},
		qqRow{Options: map[string]any{"sessionKey": json.RawMessage(`"\ud800"`)}, Actions: []qqAction{start, waitSleep, poll, {Kind: "cancel"}, poll}, Replies: []qqReply{create("raw-key-task"), status(1)}},
		qqRow{Options: map[string]any{"source": json.RawMessage(`"\ud800"`)}, Actions: []qqAction{start, poll}, Replies: []qqReply{create("bad-source")}},
	)
	for _, n := range []int64{601000, 601001} {
		n := n
		rows = append(rows, qqRow{Actions: []qqAction{start, waitSleep, {Kind: "poll", Now: &n}, poll}, Replies: []qqReply{create("ttl-task"), status(1)}})
	}
	for _, s := range []any{nil, false, 17, []any{}, map[string]any{}, ""} {
		rows = append(rows, qqRow{Options: map[string]any{"source": s}, Actions: []qqAction{start, waitSleep, poll, {Kind: "cancel"}}, Replies: []qqReply{create("private-task"), status(1)}})
	}
	for _, r := range []qqReply{{Status: 302, Redirect: true}, {Status: 500, Body: map[string]any{}}, {Body: map[string]any{"retcode": 1, "msg": "create failed"}}, {Body: map[string]any{"retcode": "0"}}, {Body: map[string]any{"retcode": 0, "data": map[string]any{"task_id": ""}}}} {
		rows = append(rows, qqRow{Actions: []qqAction{start, poll}, Replies: []qqReply{r}})
	}
	for _, raw := range []string{"null", "false", "17", "[]", "\"private-string\"", "not-json", "{", "\ufeff{\"retcode\":0}"} {
		rows = append(rows, qqRow{Actions: []qqAction{start, poll}, Replies: []qqReply{{Raw: raw}}})
	}
	for _, raw := range []string{"null", "false", "17", "[]", "not-json"} {
		rows = append(rows, qqRow{Actions: []qqAction{start, waitSleep, poll, tick, waitSuccess, poll}, Replies: []qqReply{create("private-task"), {Raw: raw}, {Secret: &secret}}})
	}
	for _, raw := range []any{false, 17, []any{}, []any{1, 2, 3}, map[string]any{}, map[string]any{"type": "Buffer", "data": []any{1, 2, 3}}, map[string]any{"length": 3, "0": 1, "1": 2, "2": 3}} {
		rows = append(rows, qqRow{Actions: []qqAction{start, waitFailed, poll}, Replies: []qqReply{create("private-task"), {Body: map[string]any{"retcode": 0, "data": map[string]any{"status": 2, "bot_appid": "private-app", "bot_encrypt_secret": raw}}}}})
	}
	for _, n := range []any{nil, false, "2", map[string]any{}, 0, 1} {
		rows = append(rows, qqRow{Actions: []qqAction{start, waitSleep, poll, tick, waitSuccess, poll}, Replies: []qqReply{create("private-task"), status(n), {Secret: &secret}}})
	}
	cert, key := qqCertificate(t)
	var expected []map[string]any
	runQQSource(t, "native-qq-registration.fixture.ts", "CC_HAHA_QQ_REGISTRATION_FIXTURE", map[string]any{"rows": rows, "cert": cert, "key": key}, &expected)
	if len(expected) != len(rows) {
		t.Fatal("Source row count")
	}
	for index, row := range rows {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			var mu sync.Mutex
			at := 0
			requests := []any{}
			held := map[int]chan struct{}{}
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, e := io.ReadAll(r.Body)
				if e != nil {
					return
				}
				var decoded any
				_ = json.Unmarshal(body, &decoded)
				mu.Lock()
				i := at
				at++
				requests = append(requests, map[string]any{"url": "https://q.qq.com" + r.URL.Path, "method": r.Method, "contentType": r.Header.Get("Content-Type"), "accept": r.Header.Get("Accept"), "contentLength": float64(len(body)), "userAgent": r.Header.Get("User-Agent"), "acceptEncoding": r.Header.Get("Accept-Encoding"), "connection": r.Header.Get("Connection"), "body": decoded})
				if i >= len(row.Replies) {
					mu.Unlock()
					t.Error("unexpected physical HTTP request")
					w.WriteHeader(500)
					return
				}
				reply := row.Replies[i]
				if reply.Hold {
					held[i] = make(chan struct{})
				}
				release := held[i]
				mu.Unlock()
				if release != nil {
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				payload := reply.Body
				if reply.Secret != nil {
					id := reply.AppID
					if id == nil {
						id = "private-app"
					}
					payload = map[string]any{"retcode": 0, "data": map[string]any{"status": 2, "bot_appid": id, "bot_encrypt_secret": qqEncryptFixture(t, make([]byte, 32), make([]byte, 12), []byte(*reply.Secret)), "user_openid": reply.UserOpenID}}
				}
				if reply.Redirect {
					w.Header().Set("Location", "https://"+r.Host+"/escaped")
				}
				w.Header().Set("Content-Type", "application/json")
				code := reply.Status
				if code == 0 {
					code = 200
				}
				w.WriteHeader(code)
				if reply.Raw != "" {
					io.WriteString(w, reply.Raw)
				} else {
					json.NewEncoder(w).Encode(payload)
				}
			}))
			clock := newQQClock()
			service, e := NewQQ(QQOptions{Client: qqFixtureClient(peer), Now: clock.Now, UUID: func() string { return "private-fixed-key" }, Random: strings.NewReader(strings.Repeat("\x00", 32*len(row.Replies))), Schedule: clock.Schedule})
			if e != nil {
				t.Fatal(e)
			}
			defer peer.Close()
			defer func() {
				mu.Lock()
				for _, c := range held {
					select {
					case <-c:
					default:
						close(c)
					}
				}
				mu.Unlock()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if e := service.Close(ctx); e != nil {
					t.Error(e)
				}
			}()
			key := encoded("private-fixed-key")
			opts := record()
			if row.Options != nil {
				b, _ := json.Marshal(row.Options)
				_ = opts.UnmarshalJSON(b)
				if v := opts.Get("sessionKey"); len(v) > 0 {
					key = v
				}
			}
			opts.Set("sessionKey", key)
			type startResult struct {
				v protocol.JSONObject
				e error
			}
			var started chan startResult
			var entry *qqSession
			values := []any{}
			for _, action := range row.Actions {
				if action.Now != nil {
					clock.mu.Lock()
					clock.now = *action.Now
					clock.mu.Unlock()
				}
				if action.Kind == "wait" {
					qqWait(t, func() bool {
						if action.Target == "sleep" {
							return clock.has(2 * time.Second)
						}
						if action.Target == "request" {
							mu.Lock()
							defer mu.Unlock()
							return held[action.Index] != nil
						}
						service.mu.Lock()
						defer service.mu.Unlock()
						if entry == nil {
							for _, v := range service.logins {
								entry = v
								break
							}
						}
						if entry == nil {
							mu.Lock()
							finished := at == len(row.Replies)
							mu.Unlock()
							return finished && action.Target == "failed"
						}
						if action.Target == "qr" {
							return entry.urlVersion >= action.Count
						}
						if action.Target == "success" {
							return entry.status == "success"
						}
						return entry.status == "failed" || entry.status == "expired"
					})
					continue
				}
				if action.Kind == "tick" {
					d := action.Duration
					if d == 0 {
						d = 2000
					}
					clock.fire(t, time.Duration(d)*time.Millisecond)
					continue
				}
				if action.Kind == "release" {
					mu.Lock()
					c := held[action.Index]
					delete(held, action.Index)
					mu.Unlock()
					close(c)
					continue
				}
				var v protocol.JSONObject
				var err error
				switch action.Kind {
				case "start_async":
					started = make(chan startResult, 1)
					go func() { v, e := service.Start(context.Background(), opts); started <- startResult{v, e} }()
					continue
				case "await_start":
					r := <-started
					v, err = r.v, r.e
				case "start":
					actual := opts.Clone()
					if action.Options != nil {
						b, _ := json.Marshal(action.Options)
						var extra protocol.JSONObject
						_ = extra.UnmarshalJSON(b)
						actual.Assign(extra)
					}
					v, err = service.Start(context.Background(), actual)
					service.mu.Lock()
					for _, x := range service.logins {
						entry = x
						break
					}
					service.mu.Unlock()
				case "cancel":
					err = service.CancelJSON(key)
					v = record(field("cancelled", true))
				default:
					v, err = service.PollJSON(context.Background(), key)
				}
				values = append(values, qqValue(v, err))
			}
			// Wait for any released, pre-ready create request to reach its actual
			// cancelled vendor branch before capturing the final physical request set.
			if started != nil {
				service.workers.Wait()
			}
			mu.Lock()
			got := map[string]any{"values": values, "requests": requests}
			mu.Unlock()
			if !reflect.DeepEqual(got, expected[index]) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(expected[index])
				t.Fatalf("Source row%d\nGo%s\nSource%s", index, a, b)
			}
		})
	}
	t.Logf("%d actual Source façade + installed vendor TLS HTTP/rotation/AES/cancel/TTL profiles", len(rows))
}
