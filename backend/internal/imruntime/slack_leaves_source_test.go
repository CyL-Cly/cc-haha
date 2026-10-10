package imruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type slackRelocate struct {
	root  string
	inner http.RoundTripper
}

func (r slackRelocate) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	copy.URL = &u
	copy.URL.Scheme = "http"
	copy.URL.Host = strings.TrimPrefix(r.root, "http://")
	copy.Host = req.URL.Host
	return r.inner.RoundTrip(copy)
}
func TestSlackActualSourceExtractManifestAndPrivateHTTPUploadProtocol(t *testing.T) {
	events := []any{nil, map[string]any{"type": "hello"}}
	base := map[string]any{"type": "message", "channel_type": "im", "channel": " D1 ", "user": " U1 ", "text": "<@U12> &amp;lt; &lt; &gt; &amp; <#C1|general> <https://example.test/a|link> <http://example.test/> ", "ts": "1"}
	events = append(events, base)
	for _, patch := range []map[string]any{{"subtype": "file_share", "files": []any{nil, map[string]any{"url_private": "https://files.slack.com/x"}}}, {"subtype": "message_changed"}, {"hidden": true}, {"bot_id": "B1"}, {"user": "UBOT"}, {"channel_type": "channel"}, {"text": " ", "files": []any{map[string]any{"url_private": "https://files.slack.com/f"}}}, {"client_msg_id": " key ", "thread_ts": " parent "}} {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range patch {
			m[k] = v
		}
		events = append(events, m)
	}
	urls := []map[string]any{}
	for _, u := range []string{"", "https://files.slack.com:443/x#secret", "https://FILES.SLACK.COM", "https://evil.test/x", "http://files.slack.com/a", "https://files.slack.com:444/a", "https://user@files.slack.com/a"} {
		for _, upload := range []bool{false, true} {
			urls = append(urls, map[string]any{"value": u, "upload": upload})
		}
	}
	actions := []map[string]any{{"kind": "post", "channel": "D1", "text": "reply", "thread": "t1"}, {"kind": "update", "channel": "D1", "ts": "1.1", "text": "edit"}, {"kind": "socket"}, {"kind": "upload", "channel": "D1", "bytes": []byte{1, 2, 3}, "name": "a b.png", "title": "", "thread": "t1"}, {"kind": "download", "url": "https://files.slack.com/f"}, {"kind": "post", "channel": "D1", "text": "failure"}, {"kind": "socket"}}
	// []byte encodes base64; fixture receives integer arrays as actual Uint8Array.
	actions[3]["bytes"] = []int{1, 2, 3}
	responses := []map[string]any{{"data": map[string]any{"ok": true, "ts": "1.1"}}, {"data": map[string]any{"ok": true}, "status": 500}, {"data": map[string]any{"ok": true, "url": "wss://localhost/socket"}}, {"data": map[string]any{"ok": true, "upload_url": "https://files.slack.com/upload#secret", "file_id": "F1"}}, {"raw": "uploaded"}, {"data": map[string]any{"ok": true}}, {"raw": "abc"}, {"data": map[string]any{"ok": false, "error": "ratelimited"}, "status": 429}, {"data": map[string]any{"ok": true, "url": "ws://insecure/socket"}}}
	want := runIMSource(t, "native-slack-leaves.fixture.ts", "CC_HAHA_SLACK_LEAVES_FIXTURE", map[string]any{"events": events, "urls": urls, "actions": actions, "responses": responses}, "")
	calls := []any{}
	n := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body any
		if len(data) > 0 {
			if r.URL.Path == "/upload" {
				bytes := []int{}
				for _, v := range data {
					bytes = append(bytes, int(v))
				}
				body = bytes
			} else {
				_ = json.Unmarshal(data, &body)
			}
		}
		headers := map[string]string{}
		if v := r.Header.Get("Authorization"); v != "" {
			headers["authorization"] = v
		}
		if v := r.Header.Get("Content-Type"); v != "" {
			headers["content-type"] = v
		}
		calls = append(calls, map[string]any{"url": "https://" + r.Host + r.URL.RequestURI(), "method": r.Method, "headers": headers, "body": body})
		reply := responses[n]
		n++
		status := 200
		if v, ok := reply["status"].(int); ok {
			status = v
		}
		w.WriteHeader(status)
		if s, ok := reply["raw"].(string); ok {
			_, _ = w.Write([]byte(s))
		} else {
			_, _ = w.Write(raw(reply["data"]))
		}
	}))
	defer peer.Close()
	api, _ := NewSlackAPI(SlackAPIOptions{Token: "private-bot", HTTP: &http.Client{Transport: slackRelocate{peer.URL, http.DefaultTransport}}})
	results := []any{}
	ctx := context.Background()
	for _, a := range actions {
		var v any
		var err error
		switch a["kind"] {
		case "post":
			v, err = api.PostMessage(ctx, a["channel"].(string), NewText(a["text"].(string)), str(a["thread"]))
		case "update":
			err = api.UpdateMessage(ctx, a["channel"].(string), a["ts"].(string), NewText(a["text"].(string)))
		case "socket":
			v, err = api.OpenSocketConnection(ctx, "private-app")
		case "upload":
			title := ""
			err = api.UploadFile(ctx, SlackUpload{Channel: "D1", Buffer: []byte{1, 2, 3}, Filename: "a b.png", Title: &title, Thread: "t1"})
		case "download":
			var b []byte
			b, err = api.DownloadFile(ctx, a["url"].(string))
			if err == nil {
				values := []int{}
				for _, c := range b {
					values = append(values, int(c))
				}
				v = values
			}
		}
		if err != nil {
			var status any
			if e, ok := err.(*SlackAPIError); ok && e.Status != 0 {
				status = e.Status
			}
			v = map[string]any{"error": err.Error(), "status": status}
		}
		results = append(results, v)
	}
	payloads := []any{}
	for _, e := range events {
		payloads = append(payloads, ExtractSlackPayload(object(raw(e)), "UBOT"))
	}
	urlResults := []any{}
	for _, u := range urls {
		v, err := AssertSlackFileURL(u["value"].(string), u["upload"].(bool))
		if err != nil {
			urlResults = append(urlResults, map[string]any{"error": err.Error()})
		} else {
			urlResults = append(urlResults, v)
		}
	}
	exactJSON(t, raw(map[string]any{"payloads": payloads, "urls": urlResults, "manifest": SlackManifestJSON(), "createAppUrl": SlackCreateAppURL(), "calls": calls, "results": results}), want, "Slack original leaves + actual HTTP")
}
func str(v any) string { s, _ := v.(string); return s }
