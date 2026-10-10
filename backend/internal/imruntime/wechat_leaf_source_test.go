package imruntime

import (
	"bytes"
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
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
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type wechatLeafReply struct {
	Status int    `json:"status,omitempty"`
	Body   any    `json:"body,omitempty"`
	Raw    string `json:"raw,omitempty"`
	Bytes  string `json:"bytes,omitempty"`
	Hold   bool   `json:"hold,omitempty"`
}
type wechatLeafAction struct {
	Method string `json:"method"`
	Status string `json:"status,omitempty"`
}
type wechatLeafRow struct {
	Candidates                                              []WechatMediaCandidate     `json:"candidates,omitempty"`
	ReplyByURL                                              map[string]wechatLeafReply `json:"replyByURL,omitempty"`
	Kind                                                    string                     `json:"kind,omitempty"`
	Method                                                  string                     `json:"method,omitempty"`
	Base, Token, Cursor, Chat, Text, Context, Ticket, Items json.RawMessage            `json:"-"`
	Status                                                  string                     `json:"status,omitempty"`
	Timeout                                                 *int64                     `json:"timeout,omitempty"`
	Actions                                                 []wechatLeafAction         `json:"actions,omitempty"`
	Replies                                                 []wechatLeafReply          `json:"replies,omitempty"`
	Candidate                                               *WechatMediaCandidate      `json:"candidate,omitempty"`
	Key                                                     string                     `json:"key,omitempty"`
}

func (r wechatLeafRow) MarshalJSON() ([]byte, error) {
	type plain wechatLeafRow
	p, _ := json.Marshal(plain(r))
	var v protocol.JSONObject
	_ = v.UnmarshalJSON(p)
	for _, f := range []struct {
		k string
		v json.RawMessage
	}{{"base", r.Base}, {"token", r.Token}, {"cursor", r.Cursor}, {"chat", r.Chat}, {"text", r.Text}, {"context", r.Context}, {"ticket", r.Ticket}, {"items", r.Items}} {
		if len(f.v) > 0 {
			v.Set(f.k, f.v)
		}
	}
	return v.MarshalJSON()
}

type wechatLeafRedirect struct {
	target  *url.URL
	base    http.RoundTripper
	observe func(*http.Request)
}

func (r wechatLeafRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "ilinkai.weixin.qq.com" && req.URL.Host != "api.wechat.test" && req.URL.Host != "cdn.wechat.test" && req.URL.Host != "findermp.video.qq.com" {
		return nil, fmt.Errorf("unowned WeChat source target")
	}
	if r.observe != nil {
		r.observe(req)
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = r.target.Scheme
	u.Host = r.target.Host
	clone.URL = &u
	return r.base.RoundTrip(clone)
}
func runWechatLeafSource(t *testing.T, root string, rows []wechatLeafRow) []map[string]any {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	fixture, _ := filepath.Abs("testdata/wechat-leaf-source.fixture.ts")
	input, _ := json.Marshal(map[string]any{"rows": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, ".claude"), "TMPDIR=" + root, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(root), "CC_HAHA_WECHAT_LEAF_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String(), string(out))
	}
	var result []map[string]any
	if e = json.Unmarshal(out, &result); e != nil {
		t.Fatal(e, string(out), stderr.String())
	}
	return result
}
func wechatLeafValue(v any) any {
	p, _ := json.Marshal(v)
	var result any
	_ = json.Unmarshal(p, &result)
	return result
}
func wechatLeafFailure(e error) string {
	if e == nil {
		return ""
	}
	s := e.Error()
	if s == "invalid JSON" {
		return "<invalid JSON>"
	}
	if strings.Contains(s, "endsWith") {
		return "<baseUrl type failure>"
	}
	if strings.Contains(s, "token") && strings.Contains(s, "trim") {
		return "<token type failure>"
	}
	if strings.Contains(s, "bad decrypt") || strings.Contains(s, "wrong final block") {
		return "<decrypt failed>"
	}
	return s
}
func TestWechatLeafActualSourceHTTPHeadersRawCredentialsProtocolAndContextRetry(t *testing.T) {
	rows := []wechatLeafRow{}
	for _, method := range []string{"updates", "text", "config", "typing"} {
		for _, body := range []any{map[string]any{"ret": 0, "typing_ticket": "private-ticket"}, map[string]any{"ret": 3, "errcode": 4, "errmsg": "private error"}, map[string]any{"ret": "3", "errcode": 4, "errmsg": 17}, nil, false, []any{}} {
			rows = append(rows, wechatLeafRow{Method: method, Context: raw("private-context"), Cursor: raw("private-cursor"), Ticket: raw("private-ticket"), Replies: []wechatLeafReply{{Body: body, Raw: map[bool]string{true: "null", false: ""}[body == nil]}}})
		}
		for _, r := range []wechatLeafReply{{Raw: "invalid JSON"}, {Raw: ""}, {Status: 503, Raw: "private HTTP error"}, {Raw: "\ufeff{\"ret\":0}"}} {
			if r.Raw == "" {
				r.Raw = " "
			}
			rows = append(rows, wechatLeafRow{Method: method, Replies: []wechatLeafReply{r}})
		}
	}
	for _, base := range []json.RawMessage{raw(17), json.RawMessage(`1e400`), raw(nil), raw(false), raw(map[string]any{}), raw([]any{}), raw("https://API.WECHAT.test/gateway?x=1")} {
		rows = append(rows, wechatLeafRow{Method: "updates", Base: base, Replies: []wechatLeafReply{{Body: map[string]any{"ret": 0}}}})
	}
	for _, token := range []json.RawMessage{raw(17), json.RawMessage(`1e400`), raw(false), raw(0), raw(map[string]any{}), raw([]any{}), raw(nil), raw(""), raw(" \ufeff private-token \ufeff ")} {
		rows = append(rows, wechatLeafRow{Method: "text", Token: token, Replies: []wechatLeafReply{{Body: map[string]any{"ret": 0}}}})
	}
	rows = append(rows, wechatLeafRow{Method: "text", Text: json.RawMessage(`"\ud800<>&汉😀"`), Chat: json.RawMessage(`"\ud801"`), Replies: []wechatLeafReply{{Body: map[string]any{"ret": 0}}}}, wechatLeafRow{Method: "text", Text: raw(""), Replies: []wechatLeafReply{{Body: map[string]any{"ret": 0}}}})
	for _, cursor := range []json.RawMessage{nil, raw(nil), raw(false), raw(17)} {
		timeout := int64(5)
		rows = append(rows, wechatLeafRow{Method: "updates", Cursor: cursor, Timeout: &timeout, Replies: []wechatLeafReply{{Hold: true}}})
	}
	rows = append(rows, wechatLeafRow{Method: "text", Replies: []wechatLeafReply{{Raw: `{"ret":1e-400,"errcode":9}`}}})
	rows = append(rows, wechatLeafRow{Method: "text", Cursor: json.RawMessage(`1e400`), Chat: json.RawMessage(`9007199254740993`), Replies: []wechatLeafReply{{Raw: `{"ret":1e400,"errmsg":"overflow"}`}}})
	long := strings.Repeat("a", 3499) + "😀" + strings.Repeat(" tail", 300)
	rows = append(rows, wechatLeafRow{Method: "sender", Text: raw(long), Context: raw("stale-context"), Replies: []wechatLeafReply{{Body: map[string]any{"ret": 9, "errmsg": "stale context"}}, {Body: map[string]any{"ret": 0}}, {Body: map[string]any{"ret": 0}}}})
	root := t.TempDir()
	expected := runWechatLeafSource(t, root, rows)
	if len(expected) != len(rows) {
		t.Fatal("Source row count")
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var mu sync.Mutex
			requests := []any{}
			at := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				if at >= len(row.Replies) {
					mu.Unlock()
					t.Error("unexpected actual WeChat HTTP")
					w.WriteHeader(500)
					return
				}
				reply := row.Replies[at]
				at++
				mu.Unlock()
				if reply.Hold {
					// Consume the actual POST body so net/http starts its peer EOF
					// monitor; an unread request body would hold test cleanup forever.
					_, _ = io.Copy(io.Discard, r.Body)
					<-r.Context().Done()
					return
				}
				status := reply.Status
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				if reply.Raw != "" {
					io.WriteString(w, reply.Raw)
				} else {
					p, _ := json.Marshal(reply.Body)
					w.Write(p)
				}
			}))
			defer peer.Close()
			target, _ := url.Parse(peer.URL)
			client := &http.Client{Transport: wechatLeafRedirect{target, peer.Client().Transport, func(req *http.Request) {
				p, _ := io.ReadAll(req.Body)
				req.Body = io.NopCloser(bytes.NewReader(p))
				headers := map[string]any{}
				for k, v := range req.Header {
					headers[strings.ToLower(k)] = strings.Join(v, ", ")
				}
				_, timed := req.Context().Deadline()
				mu.Lock()
				requests = append(requests, map[string]any{"url": req.URL.String(), "method": req.Method, "body": string(p), "headers": headers, "timed": timed})
				mu.Unlock()
			}}}
			base := row.Base
			token := row.Token
			if len(token) == 0 {
				token = raw(" private-token ")
			}
			api, e := NewWechatAPI(WechatAPIOptions{BaseURL: base, Token: token, HTTP: client, UUID: func() string { return "private-fixed-uuid" }, Random: bytes.NewReader(bytes.Repeat([]byte{42}, 4096))})
			if e != nil {
				t.Fatal(e)
			}
			defer api.Close()
			contexts := NewWechatContexts()
			contexts.Set("private-chat", row.Context)
			warnings := []string{}
			sender := &WechatSender{api, contexts, func(s string) { warnings = append(warnings, s) }}
			chat := row.Chat
			if len(chat) == 0 {
				chat = raw("private-chat")
			}
			text := row.Text
			if len(text) == 0 {
				text = raw("Source <汉😀>")
			}
			status := row.Status
			if status == "" {
				status = "typing"
			}
			overrides := []time.Duration{}
			if row.Timeout != nil {
				overrides = append(overrides, time.Duration(*row.Timeout)*time.Millisecond)
			}
			var value any
			var err error
			switch row.Method {
			case "updates":
				var v json.RawMessage
				v, err = api.GetUpdates(context.Background(), row.Cursor, overrides...)
				if err == nil {
					_ = json.Unmarshal(v, &value)
				}
			case "text":
				err = api.SendText(context.Background(), chat, TextJSON(text), row.Context, overrides...)
			case "config":
				var v json.RawMessage
				v, err = api.GetConfig(context.Background(), chat, row.Context, overrides...)
				if err == nil {
					_ = json.Unmarshal(v, &value)
				}
			case "typing":
				err = api.SendTyping(context.Background(), chat, row.Ticket, status, overrides...)
			case "sender":
				err = sender.Send(context.Background(), "private-chat", TextJSON(text))
			}
			values := []any{map[string]any{"value": value}}
			if err != nil {
				values = []any{map[string]any{"error": wechatLeafFailure(err)}}
			}
			var ctx any
			_ = json.Unmarshal(contexts.Get("private-chat"), &ctx)
			mu.Lock()
			got := map[string]any{"values": values, "requests": requests, "warnings": wechatLeafValue(warnings), "ticket": nil, "context": ctx}
			mu.Unlock()
			if !reflect.DeepEqual(got, expected[i]) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(expected[i])
				t.Fatalf("Source row%d\nGo%s\nSource%s", i, a, b)
			}
		})
	}
	t.Logf("%d actual Source API/raw credential/HTTP/body/split/contextretry cases", len(rows))
}
func TestWechatLeafActualSourceExtractionCandidatesAESAndPhysicalFiles(t *testing.T) {
	rows := []wechatLeafRow{}
	for _, items := range []json.RawMessage{raw(nil), raw([]any{}), raw([]any{map[string]any{"type": 1, "text_item": map[string]any{"text": false}, "ref_msg": map[string]any{"title": "quote", "message_item": map[string]any{"type": 1, "text_item": map[string]any{"text": 17}}}}}), raw([]any{map[string]any{"type": 3, "voice_item": map[string]any{"text": 17}}}), json.RawMessage(`[{"type":1,"text_item":{"text":"\ud800"}}]`), raw([]any{map[string]any{"type": 2, "msg_id": 0, "image_item": map[string]any{"aeskey": "001122zz", "media": map[string]any{"full_url": "https://cdn.wechat.test/img", "aes_key": "ignored", "encrypt_query_param": "k=1"}}}, map[string]any{"type": 4, "file_item": map[string]any{"file_name": "../file.TXT", "media": map[string]any{"aes_key": "key"}}}, map[string]any{"type": 5, "video_item": map[string]any{"media": map[string]any{}}}})} {
		rows = append(rows, wechatLeafRow{Method: "extract", Items: items}, wechatLeafRow{Method: "candidates", Items: items})
	}
	key := []byte("0123456789abcdef")
	highASCII := []byte(hex.EncodeToString(key))
	for i := range highASCII {
		highASCII[i] |= 0x80
	}
	rows = append(rows, wechatLeafRow{Method: "key", Key: base64.StdEncoding.EncodeToString(highASCII)})
	for _, k := range []string{base64.StdEncoding.EncodeToString(key), base64.RawURLEncoding.EncodeToString(key), "!!!" + base64.StdEncoding.EncodeToString(key), base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(key))), base64.StdEncoding.EncodeToString([]byte("short"))} {
		rows = append(rows, wechatLeafRow{Method: "key", Key: k})
	}
	plain := []byte("Actual private WeChat file 汉😀")
	pad := 16 - len(plain)%16
	padded := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	encrypted := make([]byte, len(padded))
	block, _ := aes.NewCipher(key)
	for i := 0; i < len(padded); i += 16 {
		block.Encrypt(encrypted[i:i+16], padded[i:i+16])
	}
	cases := []struct {
		candidate WechatMediaCandidate
		reply     wechatLeafReply
	}{
		{WechatMediaCandidate{Kind: "file", Name: raw("../private.TXT"), URL: raw("https://cdn.wechat.test/file")}, wechatLeafReply{Bytes: base64.StdEncoding.EncodeToString(plain)}},
		{WechatMediaCandidate{Kind: "image", Name: json.RawMessage(`"\ud800.jpg"`), EncryptQueryParam: raw("enc=private&x=1"), AESKey: raw(base64.StdEncoding.EncodeToString(key))}, wechatLeafReply{Bytes: base64.StdEncoding.EncodeToString(encrypted)}},
		{WechatMediaCandidate{Kind: "file", Name: raw("private.bin")}, wechatLeafReply{}},
		{WechatMediaCandidate{Kind: "file", Name: raw("private.bin"), URL: raw("https://cdn.wechat.test/private")}, wechatLeafReply{Status: 403, Raw: "private denied"}},
		{WechatMediaCandidate{Kind: "file", Name: raw("private.bin"), URL: raw("https://cdn.wechat.test/private"), AESKey: raw("bad")}, wechatLeafReply{Bytes: base64.StdEncoding.EncodeToString(encrypted)}},
		{WechatMediaCandidate{Kind: "file", Name: raw("private.bin"), URL: raw("https://cdn.wechat.test/private"), AESKey: raw(base64.StdEncoding.EncodeToString(key))}, wechatLeafReply{Bytes: base64.StdEncoding.EncodeToString([]byte("invalidcipher"))}},
	}
	for _, c := range cases {
		candidate := c.candidate
		rows = append(rows, wechatLeafRow{Method: "download", Candidate: &candidate, Replies: []wechatLeafReply{c.reply}})
	}
	root := t.TempDir()
	expected := runWechatLeafSource(t, root, rows)
	if len(expected) != len(rows) {
		t.Fatal("Source row count")
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var mu sync.Mutex
			requests := []any{}
			at := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				reply := row.Replies[at]
				at++
				mu.Unlock()
				status := reply.Status
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				if reply.Bytes != "" {
					p, _ := base64.StdEncoding.DecodeString(reply.Bytes)
					w.Write(p)
				} else {
					io.WriteString(w, reply.Raw)
				}
			}))
			defer peer.Close()
			target, _ := url.Parse(peer.URL)
			store := NewAttachmentStore(root)
			store.Now = func() time.Time { return time.UnixMilli(1000) }
			store.Random = func() string { return "9" }
			media, e := NewWechatMedia(WechatMediaOptions{Store: store, HTTP: &http.Client{Transport: wechatLeafRedirect{target, peer.Client().Transport, func(req *http.Request) {
				mu.Lock()
				requests = append(requests, map[string]any{"url": req.URL.String(), "method": req.Method, "body": "", "headers": map[string]any{}, "timed": false})
				mu.Unlock()
			}}}})
			if e != nil {
				t.Fatal(e)
			}
			defer media.Close()
			var value any
			var err error
			switch row.Method {
			case "extract":
				var v json.RawMessage
				v, err = WechatExtractText(row.Items)
				if err == nil {
					json.Unmarshal(v, &value)
				}
			case "candidates":
				var c []WechatMediaCandidate
				c, err = WechatCollectMediaCandidates(row.Items, func() time.Time { return time.UnixMilli(1000) })
				value = wechatLeafValue(c)
			case "key":
				var key []byte
				key, err = WechatAESKey(row.Key)
				value = hex.EncodeToString(key)
			case "download":
				var local WechatLocalAttachment
				local, err = media.DownloadCandidate(context.Background(), *row.Candidate, "private-session")
				if err == nil {
					bytes, e := os.ReadFile(local.Path)
					if e != nil {
						t.Fatal(e)
					}
					info, e := os.Stat(local.Path)
					if e != nil {
						t.Fatal(e)
					}
					var name any
					json.Unmarshal(local.NameJSON, &name)
					value = map[string]any{"kind": local.Kind, "name": name, "path": local.Path, "size": float64(local.Size), "mimeType": local.MIME, "bytes": base64.StdEncoding.EncodeToString(local.Buffer), "physical": base64.StdEncoding.EncodeToString(bytes), "mode": float64(info.Mode().Perm())}
					os.RemoveAll(store.Root)
				}
			}
			values := []any{map[string]any{"value": value}}
			if err != nil {
				values = []any{map[string]any{"error": wechatLeafFailure(err)}}
			}
			mu.Lock()
			got := map[string]any{"values": values, "requests": requests, "warnings": []any{}, "ticket": nil, "context": nil}
			mu.Unlock()
			if !reflect.DeepEqual(got, expected[i]) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(expected[i])
				t.Fatalf("actual Source media row%d\nGo%s\nSource%s", i, a, b)
			}
		})
	}
}

func TestWechatLeafActualSourceTypingTicketRetryCacheAndKeepalive(t *testing.T) {
	ticket := map[string]any{"ret": 0, "typing_ticket": "private-ticket"}
	ok := map[string]any{"ret": 0}
	rows := []wechatLeafRow{
		{Method: "indicator", Context: raw("private-context"), Actions: []wechatLeafAction{{Method: "indicator"}, {Method: "indicator"}, {Method: "indicator", Status: "cancel"}}, Replies: []wechatLeafReply{{Body: ticket}, {Body: ok}, {Body: ok}, {Body: ok}}},
		{Method: "indicator", Actions: []wechatLeafAction{{Method: "indicator", Status: "cancel"}}},
		{Method: "indicator", Actions: []wechatLeafAction{{Method: "indicator"}, {Method: "indicator"}}, Replies: []wechatLeafReply{{Body: ticket}, {Body: map[string]any{"ret": 9, "errmsg": "old ticket"}}, {Body: map[string]any{"typing_ticket": "new-ticket"}}, {Body: ok}, {Body: ok}}},
		{Method: "indicator", Actions: []wechatLeafAction{{Method: "indicator"}, {Method: "indicator", Status: "cancel"}}, Replies: []wechatLeafReply{{Body: ticket}, {Body: ok}, {Body: map[string]any{"ret": 9, "errmsg": "cancel failed"}}}},
		{Method: "indicator", Actions: []wechatLeafAction{{Method: "indicator"}}, Replies: []wechatLeafReply{{Body: map[string]any{"ret": 9, "errmsg": "config failed"}}, {Body: map[string]any{"ret": 7, "errmsg": "retry failed"}}}},
		{Method: "indicator", Actions: []wechatLeafAction{{Method: "indicator"}}, Replies: []wechatLeafReply{{Raw: `{"ret":1e400,"errmsg":"overflow"}`}, {Raw: `{"ret":1e400,"errmsg":"overflow retry"}`}}},
		{Method: "indicator", Actions: []wechatLeafAction{{Method: "indicator"}, {Method: "indicator", Status: "cancel"}}, Replies: []wechatLeafReply{{Body: map[string]any{"ret": 0}}}},
		{Method: "indicator", Actions: []wechatLeafAction{{Method: "indicator"}}, Replies: []wechatLeafReply{{Body: map[string]any{"ret": "9", "typing_ticket": 17}}, {Body: ok}}},
		{Kind: "typing-controller", Actions: []wechatLeafAction{{Method: "start"}, {Method: "start"}, {Method: "tick"}, {Method: "stop"}, {Method: "stop"}, {Method: "start"}, {Method: "destroy"}}, Replies: []wechatLeafReply{{Body: ticket}, {Body: ok}, {Body: ok}, {Body: ok}, {Body: ok}, {Body: ok}, {Body: ok}}},
	}
	root := t.TempDir()
	expected := runWechatLeafSource(t, root, rows)
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var mu sync.Mutex
			requests := []any{}
			at := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				if at >= len(row.Replies) {
					mu.Unlock()
					t.Error("unexpected typing physical HTTP")
					w.WriteHeader(500)
					return
				}
				reply := row.Replies[at]
				at++
				mu.Unlock()
				if reply.Raw != "" {
					_, _ = io.WriteString(w, reply.Raw)
				} else {
					p, _ := json.Marshal(reply.Body)
					_, _ = w.Write(p)
				}
			}))
			defer peer.Close()
			target, _ := url.Parse(peer.URL)
			api, e := NewWechatAPI(WechatAPIOptions{Token: raw("private-token"), HTTP: &http.Client{Transport: wechatLeafRedirect{target, peer.Client().Transport, func(req *http.Request) {
				p, _ := io.ReadAll(req.Body)
				req.Body = io.NopCloser(bytes.NewReader(p))
				headers := map[string]any{}
				for k, v := range req.Header {
					headers[strings.ToLower(k)] = strings.Join(v, ", ")
				}
				mu.Lock()
				requests = append(requests, map[string]any{"url": req.URL.String(), "method": req.Method, "body": string(p), "headers": headers, "timed": true})
				mu.Unlock()
			}}}, Random: bytes.NewReader(bytes.Repeat([]byte{42}, 4096))})
			if e != nil {
				t.Fatal(e)
			}
			defer api.Close()
			contexts := NewWechatContexts()
			contexts.Set("private-chat", row.Context)
			warnings := []string{}
			timers := []func(){}
			canceled := map[int]bool{}
			ctl := NewWechatTypingController(context.Background(), api, contexts, WechatTypingOptions{Log: func(s string) { warnings = append(warnings, s) }, Schedule: func(d time.Duration, f func()) func() {
				if d != 5*time.Second {
					t.Error("Source 5000ms keepalive changed")
				}
				i := len(timers)
				timers = append(timers, f)
				return func() { canceled[i] = true }
			}})
			defer ctl.Close()
			values := []any{}
			for _, a := range row.Actions {
				if row.Kind == "typing-controller" {
					switch a.Method {
					case "start":
						ctl.Start("private-chat")
					case "stop":
						ctl.Stop("private-chat")
					case "tick":
						for i, f := range timers {
							if !canceled[i] {
								f()
							}
						}
					case "destroy":
						ctl.Destroy()
					}
					ctl.wg.Wait()
				} else {
					status := a.Status
					if status == "" {
						status = "typing"
					}
					ctl.SendIndicator(context.Background(), "private-chat", status)
					values = append(values, map[string]any{"value": nil})
				}
			}
			if row.Kind == "typing-controller" {
				values = append(values, map[string]any{"value": map[string]any{"timers": float64(len(timers)), "stopped": float64(len(canceled))}})
			}
			var contextValue, ticketValue any
			json.Unmarshal(contexts.Get("private-chat"), &contextValue)
			json.Unmarshal(ctl.Ticket("private-chat"), &ticketValue)
			mu.Lock()
			got := map[string]any{"values": values, "requests": requests, "warnings": wechatLeafValue(warnings), "ticket": ticketValue, "context": contextValue}
			mu.Unlock()
			if !reflect.DeepEqual(got, expected[i]) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(expected[i])
				t.Fatalf("original typing helper/controller row%d\nGo%s\nSource%s", i, a, b)
			}
		})
	}
}

func TestWechatLeafActualSourceDeferredParallelCollectorReferencesAndPhysicalFiles(t *testing.T) {
	candidates := []WechatMediaCandidate{{Kind: "image", Name: raw("private.jpg"), MIME: "image/jpeg", URL: raw("https://cdn.wechat.test/image")}, {Kind: "file", Name: raw("failed.txt"), URL: raw("https://cdn.wechat.test/failure")}, {Kind: "file", Name: raw("private.txt"), MIME: "text/plain", URL: raw("https://cdn.wechat.test/file")}}
	rows := []wechatLeafRow{{Method: "collect", Candidates: candidates, ReplyByURL: map[string]wechatLeafReply{"https://cdn.wechat.test/image": {Bytes: base64.StdEncoding.EncodeToString([]byte("image bytes"))}, "https://cdn.wechat.test/failure": {Status: 403, Raw: "private denied"}, "https://cdn.wechat.test/file": {Bytes: base64.StdEncoding.EncodeToString([]byte("file bytes"))}}, Replies: []wechatLeafReply{{Body: map[string]any{"ret": 0}}}}}
	root := t.TempDir()
	expected := runWechatLeafSource(t, root, rows)[0]
	var mu sync.Mutex
	requests := []any{}
	wire := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		wire++
		mu.Unlock()
		logical := r.Header.Get("x-fixture-url")
		reply, ok := rows[0].ReplyByURL[logical]
		if !ok {
			reply = rows[0].Replies[0]
		}
		status := reply.Status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		if reply.Bytes != "" {
			p, _ := base64.StdEncoding.DecodeString(reply.Bytes)
			w.Write(p)
		} else if reply.Raw != "" {
			io.WriteString(w, reply.Raw)
		} else {
			p, _ := json.Marshal(reply.Body)
			w.Write(p)
		}
	}))
	defer peer.Close()
	target, _ := url.Parse(peer.URL)
	client := &http.Client{Transport: wechatLeafRedirect{target, peer.Client().Transport, func(req *http.Request) {
		body := ""
		if req.Body != nil {
			p, _ := io.ReadAll(req.Body)
			body = string(p)
			req.Body = io.NopCloser(bytes.NewReader(p))
		}
		headers := map[string]any{}
		for k, v := range req.Header {
			headers[strings.ToLower(k)] = strings.Join(v, ", ")
		}
		_, timed := req.Context().Deadline()
		mu.Lock()
		requests = append(requests, map[string]any{"url": req.URL.String(), "method": req.Method, "body": body, "headers": headers, "timed": timed})
		mu.Unlock()
		req.Header.Set("x-fixture-url", req.URL.String())
	}}}
	store := NewAttachmentStore(root)
	store.Now = func() time.Time { return time.UnixMilli(1000) }
	media, e := NewWechatMedia(WechatMediaOptions{Store: store, HTTP: client})
	if e != nil {
		t.Fatal(e)
	}
	defer media.Close()
	api, e := NewWechatAPI(WechatAPIOptions{HTTP: client, Token: raw("private-token"), UUID: func() string { return "private-fixed-uuid" }, Random: bytes.NewReader(bytes.Repeat([]byte{42}, 100))})
	if e != nil {
		t.Fatal(e)
	}
	defer api.Close()
	sender := WechatSender{API: api, Contexts: NewWechatContexts()}
	// Merely constructing the deferred callback performs no fetch or write.
	load := func() ([]protocol.JSONObject, error) {
		return media.Collect(context.Background(), candidates, "private-session", func(s string) error { return sender.Send(context.Background(), "private-chat", NewText(s)) })
	}
	if wire != 0 {
		t.Fatal("deferred media fetched before gate")
	}
	if _, e := os.Stat(store.Root); !os.IsNotExist(e) {
		t.Fatal("deferred media wrote before gate")
	}
	refs, e := load()
	if e != nil {
		t.Fatal(e)
	}
	physical := []any{}
	for _, c := range candidates {
		path := filepath.Join(store.Root, "wechat", "private-session", filepath.Base(TextJSON(c.Name).String()))
		if p, e := os.ReadFile(path); e == nil {
			physical = append(physical, map[string]any{"path": path, "bytes": base64.StdEncoding.EncodeToString(p)})
		}
	}
	got := map[string]any{"values": []any{map[string]any{"value": map[string]any{"attachments": wechatLeafValue(refs), "physical": physical}}}, "requests": requests, "warnings": []any{}, "ticket": nil, "context": nil}
	// Promise.allSettled preserves attachment ordering; parallel network arrival
	// is not ordered by either HTTP stack. Compare the full wire records by URL.
	normalize := func(v map[string]any) {
		records := v["requests"].([]any)
		sort.Slice(records, func(i, j int) bool {
			return records[i].(map[string]any)["url"].(string) < records[j].(map[string]any)["url"].(string)
		})
	}
	normalize(got)
	normalize(expected)
	if !reflect.DeepEqual(got, expected) {
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(expected)
		t.Fatalf("Source collector/allsettled/file/reference mismatch\nGo%s\nSource%s", a, b)
	}
	if wire != 4 {
		t.Fatalf("actual mixed collector + partial failure notice calls%d", wire)
	}
}

func TestWechatLeafCloseCancelsActualHTTPBodyMediaAndTypingTimers(t *testing.T) {
	for _, kind := range []string{"api", "media", "typing"} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprint(kind, partial), func(t *testing.T) {
				started := make(chan struct{}, 1)
				closed := make(chan struct{}, 1)
				release := make(chan struct{})
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					if partial {
						io.WriteString(w, `{"ret":`)
						w.(http.Flusher).Flush()
					}
					started <- struct{}{}
					select {
					case <-r.Context().Done():
						closed <- struct{}{}
					case <-release:
					}
				}))
				defer peer.Close()
				defer close(release)
				target, _ := url.Parse(peer.URL)
				client := &http.Client{Transport: wechatLeafRedirect{target, peer.Client().Transport, nil}}
				api, e := NewWechatAPI(WechatAPIOptions{HTTP: client, Token: raw("private-token")})
				if e != nil {
					t.Fatal(e)
				}
				defer api.Close()
				media, e := NewWechatMedia(WechatMediaOptions{HTTP: client, Store: NewAttachmentStore(t.TempDir())})
				if e != nil {
					t.Fatal(e)
				}
				defer media.Close()
				typing := NewWechatTypingController(context.Background(), api, NewWechatContexts(), WechatTypingOptions{})
				defer typing.Close()
				done := make(chan error, 1)
				switch kind {
				case "api":
					go func() { _, e := api.GetUpdates(context.Background(), raw("cursor")); done <- e }()
				case "media":
					go func() {
						_, e := media.DownloadCandidate(context.Background(), WechatMediaCandidate{Kind: "file", Name: raw("private.txt"), URL: raw("https://cdn.wechat.test/held")}, "session")
						done <- e
					}()
				case "typing":
					typing.Start("private-chat")
				}
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("actual private operation not submitted")
				}
				joined := make(chan struct{})
				go func() {
					if kind == "api" {
						api.Close()
					} else if kind == "media" {
						media.Close()
					} else {
						typing.Close()
					}
					close(joined)
				}()
				select {
				case <-joined:
				case <-time.After(3 * time.Second):
					t.Fatal("Close did not cancel/join physical I/O")
				}
				select {
				case <-closed:
				case <-time.After(3 * time.Second):
					t.Fatal("private socket did not observe cancellation")
				}
				if kind != "typing" {
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Fatal("caller remained pending after Close")
					}
				}
				if kind == "media" {
					if _, e := os.Stat(media.o.Store.Root); !os.IsNotExist(e) {
						t.Fatal("canceled media wrote a staged file")
					}
				}
			})
		}
	}
}
