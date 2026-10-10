package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func qqOriginal(t *testing.T, fixture string, input any) json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	home := t.TempDir()
	path, e := filepath.Abs("../../testdata/" + fixture)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_QQ_MESSAGE_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v\n%s", e, stderr.String())
	}
	return out
}

type qqReplyRow struct {
	Status      int    `json:"status,omitempty"`
	Body        any    `json:"body,omitempty"`
	Raw         string `json:"raw,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}
type qqLeafRow struct {
	Target  map[string]any   `json:"target,omitempty"`
	Actions []map[string]any `json:"actions"`
	Replies []qqReplyRow     `json:"replies"`
}
type qqHTTPRoute struct{ peer *httptest.Server }

func (r qqHTTPRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "bots.qq.com" && req.URL.Host != "api.sgroup.qq.com" {
		return nil, errors.New("unowned QQ HTTP target")
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = "http"
	u.Host = r.peer.Listener.Addr().String()
	copy.URL = &u
	copy.Header.Set("X-Fixture-Logical-URL", req.URL.String())
	return r.peer.Client().Transport.RoundTrip(copy)
}
func qqObserve(value any, e error) protocol.JSONObject {
	if e == nil {
		return qqObject(qqField("value", value))
	}
	r := qqObject(qqField("error", e.Error()))
	var api *QQAPIError
	if errors.As(e, &api) {
		r.Set("httpStatus", raw(api.HTTPStatus))
		r.Set("path", raw(api.Path))
		r.Set("bizCode", api.BizCode)
		r.Set("bizMessage", api.BizMessage)
	}
	return r
}
func TestQQActualVendorHTTPTokenMessagesAndAdapterResponse(t *testing.T) {
	token := qqReplyRow{Body: map[string]any{"access_token": "private-access-token", "expires_in": 7200}}
	ok := qqReplyRow{Body: map[string]any{"id": "private-stream-id", "ext_info": map[string]any{"ref_idx": "private-reference"}}}
	bad := qqReplyRow{Status: 400, Body: map[string]any{"code": 17, "message": "private stream broken"}}
	rows := []qqLeafRow{
		{Actions: []map[string]any{{"kind": "token"}, {"kind": "token"}, {"kind": "token", "now": 107200000}}, Replies: []qqReplyRow{token, token}},
		{Actions: []map[string]any{{"kind": "text", "text": "plain😀"}, {"kind": "typing"}}, Replies: []qqReplyRow{token, ok, ok}},
		{Target: map[string]any{"scope": "c2c", "targetId": "private-user"}, Actions: []map[string]any{{"kind": "text", "text": "plain"}, {"kind": "text", "text": "  "}, {"kind": "typing"}}, Replies: []qqReplyRow{token, ok, ok}},
		{Actions: []map[string]any{{"kind": "response"}, {"kind": "append", "text": "first😀"}, {"kind": "append", "text": " next"}, {"kind": "finish"}}, Replies: []qqReplyRow{token, ok, ok}},
		{Actions: []map[string]any{{"kind": "response"}, {"kind": "append", "text": "first"}, {"kind": "append", "text": " tail"}, {"kind": "finish"}}, Replies: []qqReplyRow{token, bad, ok}},
		{Actions: []map[string]any{{"kind": "response"}, {"kind": "append", "text": "first"}, {"kind": "append", "text": " tail", "now": 100000600}, {"kind": "append", "text": " plus"}, {"kind": "finish"}}, Replies: []qqReplyRow{token, ok, bad, ok}},
		{Actions: []map[string]any{{"kind": "response"}, {"kind": "append", "text": "first"}, {"kind": "append", "text": " tail"}, {"kind": "finish"}}, Replies: []qqReplyRow{token, ok, bad}},
		{Target: map[string]any{"scope": "c2c", "targetId": "private-user"}, Actions: []map[string]any{{"kind": "response"}, {"kind": "append", "text": " first "}, {"kind": "retarget", "target": map[string]any{"scope": "c2c", "targetId": "new-user", "msgId": "new-inbound"}}, {"kind": "append", "text": " tail "}, {"kind": "finish"}}, Replies: []qqReplyRow{token, ok}},
	}
	for _, r := range []qqReplyRow{{Status: 502, Raw: "<html>private</html>"}, {Status: 429, ContentType: "text/html", Raw: "limited"}, {Status: 400, Raw: "null"}, {Status: 400, Raw: "not-json"}, {Status: 400, Body: map[string]any{"code": 50002, "message": "too many"}}, {Status: 200, Raw: "<html>private</html>"}, {Status: 200, Raw: "invalid"}} {
		rows = append(rows, qqLeafRow{Actions: []map[string]any{{"kind": "api", "path": "/private", "body": map[string]any{"input": "x"}}}, Replies: []qqReplyRow{token, r}})
	}
	for _, r := range []qqReplyRow{{Status: 401, Raw: "private failure"}, {Body: map[string]any{}}, {Raw: "not-json"}} {
		rows = append(rows, qqLeafRow{Actions: []map[string]any{{"kind": "token"}}, Replies: []qqReplyRow{r}})
	}
	rows = append(rows, qqLeafRow{Actions: []map[string]any{{"kind": "notice", "text": string(bytes.Repeat([]byte("a"), 1501))}}, Replies: []qqReplyRow{token, ok, ok}}, qqLeafRow{Actions: []map[string]any{{"kind": "image", "base64": "cHJpdmF0ZQ==", "alt": "private-alt"}, {"kind": "image", "base64": "cHJpdmF0ZQ==", "alt": "again"}}, Replies: []qqReplyRow{token, {Body: map[string]any{"file_info": "private-info", "file_uuid": "private-uuid", "ttl": 3600}}, ok, ok}})
	want := object(qqOriginal(t, "native-qq-message-leaves.fixture.ts", map[string]any{"rows": rows}))
	expected := collectJSONArray(want.Get("rows"))
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var mu sync.Mutex
			at := 0
			calls := []protocol.JSONObject{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				var body any
				if len(data) > 0 {
					_ = json.Unmarshal(data, &body)
				}
				var authorization any
				if value := r.Header.Get("Authorization"); value != "" {
					authorization = value
				}
				mu.Lock()
				calls = append(calls, qqObject(qqField("url", r.Header.Get("X-Fixture-Logical-URL")), qqField("method", r.Method), qqField("authorization", authorization), qqField("contentType", r.Header.Get("Content-Type")), qqField("userAgent", r.Header.Get("User-Agent")), qqField("body", body)))
				if at >= len(row.Replies) {
					mu.Unlock()
					t.Error("unexpected QQ request")
					w.WriteHeader(500)
					return
				}
				reply := row.Replies[at]
				at++
				mu.Unlock()
				mime := reply.ContentType
				if mime == "" {
					mime = "application/json"
				}
				w.Header().Set("Content-Type", mime)
				status := reply.Status
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				if reply.Raw != "" {
					io.WriteString(w, reply.Raw)
				} else {
					w.Write(raw(reply.Body))
				}
			}))
			defer peer.Close()
			now := int64(100000000)
			client, e := NewQQClient(context.Background(), QQClientOptions{Credentials: qqObject(qqField("appId", " app "), qqField("appSecret", "private-secret")), HTTP: &http.Client{Transport: qqHTTPRoute{peer}}, Now: func() time.Time { return time.UnixMilli(now) }, Random: func() float64 { return 0 }})
			if e != nil {
				t.Fatal(e)
			}
			defer client.Close()
			target := QQTarget{"c2c", NewText("private-user"), NewText("private-inbound")}
			if row.Target != nil {
				target = qqTargetFixture(row.Target)
			}
			var response *QQResponse
			values := []protocol.JSONObject{}
			for _, a := range row.Actions {
				if v, ok := a["now"]; ok {
					now = int64(v.(int))
				}
				var v any
				var err error
				switch a["kind"] {
				case "token":
					v, err = client.GetAccessToken(context.Background())
				case "text":
					v, err = client.SendText(context.Background(), target, NewText(a["text"].(string)))
				case "typing":
					result, e := client.SendTyping(context.Background(), target)
					err = e
					v = qqObject(protocol.JSONField{Name: "refIdx", Value: object(object(result).Get("ext_info")).Get("ref_idx")})
				case "api":
					v, err = client.Request(context.Background(), "POST", a["path"].(string), raw(a["body"]))
				case "notice":
					err = client.SendNotice(context.Background(), target, NewText(a["text"].(string)))
				case "response":
					response = NewQQResponse(context.Background(), client, target, func(text Text) error { return client.SendNotice(context.Background(), target, text) }, nil)
				case "append":
					err = response.Append(NewText(a["text"].(string)))
				case "finish":
					err = response.Finish()
				case "retarget":
					target = qqTargetFixture(a["target"].(map[string]any))
				case "image":
					data := []byte("private")
					err = client.SendImage(context.Background(), target, OutboundImage{Buffer: data, MIME: "image/png", Alt: a["alt"].(string)})
					if err == nil {
						v = object(raw(row.Replies[at-1].Body))
					}
				}
				values = append(values, qqObserve(v, err))
			}
			mu.Lock()
			got := qqObject(qqField("values", values), qqField("calls", calls))
			mu.Unlock()
			exactJSON(t, raw(got), raw(expected[i]), fmt.Sprint(i))
		})
	}
}
func qqTargetFixture(o map[string]any) QQTarget {
	v := object(raw(o))
	return QQTarget{v.String("scope"), TextJSON(v.Get("targetId")), TextJSON(v.Get("msgId"))}
}
