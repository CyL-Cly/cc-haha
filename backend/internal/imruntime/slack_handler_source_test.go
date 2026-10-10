package imruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSlackFullOriginalIndexResponseFailuresThreadCacheAndDeferredMediaActualHTTP(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "repo")
	_ = os.MkdirAll(work, 0700)
	configDir := filepath.Join(home, ".claude")
	_ = os.MkdirAll(configDir, 0700)
	seed := map[string]any{"defaultProjectDir": work, "slack": map[string]any{"botToken": "private-bot", "appToken": "private-app", "allowedUsers": []string{"U1"}}}
	now := int64(1770000000000)
	actions := []map[string]any{{"kind": "new", "id": "a", "thread": "parent"}, {"kind": "append", "id": "a", "text": "first"}, {"kind": "append", "id": "a", "text": " pending", "now": now + 100}, {"kind": "finish", "id": "a"}, {"kind": "append", "id": "a", "text": " lost", "failure": "chat.update", "now": now + 2000}, {"kind": "finish", "id": "a"}, {"kind": "append", "id": "a", "text": strings.Repeat("中", 3700) + "😀 tail", "now": now + 4000}, {"kind": "finish", "id": "a"}, {"kind": "new", "id": "space"}, {"kind": "append", "id": "space", "text": " \n "}, {"kind": "finish", "id": "space"}, {"kind": "notice", "text": strings.Repeat("x", 3501)}, {"kind": "display", "user": "U1"}, {"kind": "display", "user": "U1"}, {"kind": "collect", "files": []any{map[string]string{"name": "photo.jpg", "mimetype": "image/jpeg;ignored", "url_private": "https://files.slack.com/image"}, map[string]string{"name": "note.txt", "url_private": "https://files.slack.com/file"}, map[string]string{"name": "unsupported.svg", "url_private": "https://files.slack.com/image"}, map[string]string{"url_private_download": "", "url_private": "https://files.slack.com/file"}, map[string]string{"url_private": "https://evil.test/x"}}}, {"kind": "image", "bytes": []int{1, 2, 3}, "mime": "image/jpeg", "alt": "private image"}, {"kind": "clear"}, {"kind": "notice", "text": "after clear"}}
	for i := 0; i < 501; i++ {
		actions = append(actions, map[string]any{"kind": "display", "user": "U" + stringNumber(i+2)})
	}
	actions = append(actions, map[string]any{"kind": "display", "user": "U1"})
	files := map[string]any{"/image": []int{1, 2, 3}, "/file": []int{97, 98, 99}, "/upload": []int{}}
	want := runIMSource(t, "native-slack-handler.fixture.ts", "CC_HAHA_SLACK_HANDLER_FIXTURE", map[string]any{"now": now, "work": work, "config": seed, "actions": actions, "files": files}, home)
	_ = os.RemoveAll(filepath.Join(home, ".claude", "im-downloads"))
	_ = os.WriteFile(filepath.Join(configDir, "adapters.json"), raw(seed), 0600)
	calls := []any{}
	post := 0
	failure := ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body any
		if len(data) > 0 {
			if r.URL.Path == "/upload" {
				b := []int{}
				for _, v := range data {
					b = append(b, int(v))
				}
				body = b
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
		if failure != "" && strings.HasSuffix(r.URL.Path, "/"+failure) {
			failure = ""
			w.WriteHeader(429)
			_, _ = w.Write(raw(map[string]any{"ok": false, "error": "private_failure"}))
			return
		}
		if r.Host == "files.slack.com" {
			values, _ := files[r.URL.Path].([]int)
			bytes := []byte{}
			for _, v := range values {
				bytes = append(bytes, byte(v))
			}
			_, _ = w.Write(bytes)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/users.info") {
			_, _ = w.Write(raw(map[string]any{"ok": true, "user": map[string]string{"real_name": " User " + r.URL.Query().Get("user") + " "}}))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/files.getUploadURLExternal") {
			_, _ = w.Write(raw(map[string]any{"ok": true, "upload_url": "https://files.slack.com/upload", "file_id": "F1"}))
			return
		}
		post++
		_, _ = w.Write(raw(map[string]any{"ok": true, "ts": stringNumber(post) + ".1"}))
	}))
	defer peer.Close()
	api, _ := NewSlackAPI(SlackAPIOptions{Token: "private-bot", HTTP: &http.Client{Transport: slackRelocate{peer.URL, http.DefaultTransport}}})
	cfg := LoadPlatformConfig("slack", home, work, map[string]string{}, func(string) {})
	runtime, err := NewSlackRuntime(context.Background(), SlackRuntimeOptions{Config: cfg, API: api, Now: func() time.Time { return time.UnixMilli(now) }})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	_ = runtime.Store.Set("D1", "bound-session", work)
	responses := map[string]*SlackResponse{}
	results := []any{}
	for _, a := range actions {
		if n, ok := a["now"].(int64); ok {
			now = n
		}
		if thread, ok := a["thread"].(string); ok {
			runtime.threads["D1"] = thread
		}
		if v, ok := a["failure"].(string); ok {
			failure = v
		}
		id := str(a["id"])
		switch a["kind"] {
		case "new":
			responses[id] = &SlackResponse{runtime: runtime, chat: "D1"}
		case "append":
			_ = responses[id].Append(NewText(a["text"].(string)))
		case "finish":
			_ = responses[id].Finish()
		case "notice":
			if err := runtime.Notice("D1", a["text"].(string)); err != nil {
				t.Fatal(err)
			}
		case "display":
			results = append(results, runtime.DisplayName(a["user"].(string)))
		case "collect":
			refs, err := runtime.CollectAttachments("D1", collectJSONArray(raw(a["files"])))
			if err != nil {
				t.Fatal(err)
			}
			results = append(results, refs)
		case "image":
			if err := runtime.SendImage("D1", OutboundImage{[]byte{1, 2, 3}, "image/jpeg", "private image"}); err != nil {
				t.Fatal(err)
			}
		case "clear":
			runtime.Chat.o.Port.ClearChat("D1")
		}
		if s := responses[id]; s != nil {
			var ts any
			if s.ts != "" {
				ts = s.ts
			}
			results = append(results, map[string]any{"ts": ts, "current": s.current, "pending": s.pending, "lastEdit": s.lastEdit})
		}
	}
	exactJSON(t, raw(map[string]any{"calls": calls, "results": results, "thread": runtime.Thread("D1"), "cacheSize": len(runtime.names)}), want, "Slack actual original index + physical API/files")
}
