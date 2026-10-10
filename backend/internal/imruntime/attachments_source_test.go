package imruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestTelegramActualSourceDownloadFileImageLimitsWatcherAndSafeOutboundPhysicalFiles(t *testing.T) {
	data := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 1, 2}
	encoded := base64.StdEncoding.EncodeToString(data)
	downloads := []map[string]any{{"id": "photo.jpg", "session": "chat-42", "hint": map[string]any{"fileName": "photo.jpg", "mimeType": "image/jpeg"}}, {"id": "a.txt", "session": "../../chat-42", "hint": map[string]any{"fileName": "../../a.txt"}}, {"id": "a.HEIC", "session": "chat-42", "hint": map[string]any{}}, {"id": "blob", "session": "chat-42", "hint": map[string]any{"fileName": "picture", "mimeType": "image/png"}}, {"id": "missing", "session": "chat-42", "hint": map[string]any{}}, {"id": "failure", "session": "chat-42", "hint": map[string]any{}}}
	chunks := []string{"prefix ![😀", " alt](/private/test.png) ![again](/private/test.png)", " ![inline](data:image/png;base64,aGVsbG8=) ![](https://example.invalid/a.png) ![](file:///private/photo.png)", " ![ignored](relative.png) ![](data:text/plain;other,test) ![](https://bad.invalid/\u00a0x)", strings.Repeat("x", 4200), " ![new](/private/new.png)"}
	outbound := []map[string]any{{"source": UploadSource{Kind: "path", Path: "$work/inside.png"}}, {"source": UploadSource{Kind: "path", Path: "$work/escape.png"}}, {"source": UploadSource{Kind: "path", Path: "$home/outside.png"}}, {"source": UploadSource{Kind: "path", Path: "$work/missing.png"}}, {"source": UploadSource{Kind: "path", Path: "$work"}}, {"source": UploadSource{Kind: "url", URL: "http://127.0.0.1/private-never-fetched"}}, {"source": UploadSource{Kind: "base64", MIME: "image/png", Data: encoded}}, {"source": UploadSource{Kind: "base64", MIME: "image/svg+xml", Data: "aGVsbG8="}}, {"source": UploadSource{Kind: "base64", MIME: "image/png", Data: " aG_VsbG8=ignored"}}, {"source": UploadSource{Kind: "base64", MIME: "image/png", Data: encoded}, "noWork": true}}
	limits := []map[string]any{{"kind": "image", "size": ImageMaxBytes, "mime": "image/png"}, {"kind": "image", "size": ImageMaxBytes + 1, "mime": "image/png"}, {"kind": "image", "size": 0, "mime": "image/heic"}, {"kind": "image", "size": 1, "mime": ""}, {"kind": "file", "size": FileMaxBytes}, {"kind": "file", "size": FileMaxBytes + 1}}
	want := runIMSource(t, "native-telegram-media.fixture.ts", "CC_HAHA_TELEGRAM_MEDIA_FIXTURE", map[string]any{"data": encoded, "downloads": downloads, "chunks": chunks, "outbound": outbound, "limits": limits}, "")
	var mu sync.Mutex
	calls := []any{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendPhoto") {
			if err := r.ParseMultipartForm(1024 * 1024); err != nil {
				t.Error(err)
				return
			}
			reference := strings.TrimPrefix(r.FormValue("photo"), "attach://")
			file, header, err := r.FormFile(reference)
			if err != nil {
				t.Error(err)
				return
			}
			data, _ := io.ReadAll(file)
			_ = file.Close()
			mu.Lock()
			calls = append(calls, map[string]any{"method": "sendPhoto", "chat": r.FormValue("chat_id"), "caption": r.FormValue("caption"), "file": map[string]any{"name": header.Filename, "mime": header.Header.Get("Content-Type"), "data": base64.StdEncoding.EncodeToString(data)}})
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			calls = append(calls, map[string]any{"method": "getFile", "payload": payload})
			mu.Unlock()
			result := map[string]any{}
			if payload["file_id"] != "missing" {
				result["file_path"] = "media/" + payload["file_id"].(string)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw(map[string]any{"ok": true, "result": result}))
			return
		}
		mu.Lock()
		calls = append(calls, map[string]any{"method": "download", "path": r.URL.Path})
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/failure") {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
	}))
	defer peer.Close()
	api, err := NewTelegramAPI(TelegramAPIOptions{Token: "private-token", APIRoot: peer.URL, HTTP: peer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	home := t.TempDir()
	store := NewAttachmentStore(home)
	store.Root = filepath.Join(home, "store")
	media := TelegramMedia{API: api, Store: store, HTTP: peer.Client()}
	actualDownloads := []any{}
	for _, profile := range downloads {
		hints := profile["hint"].(map[string]any)
		var name, mime *string
		if value, ok := hints["fileName"]; ok {
			text := value.(string)
			name = &text
		}
		if value, ok := hints["mimeType"]; ok {
			text := value.(string)
			mime = &text
		}
		attachment, err := media.Download(context.Background(), profile["id"].(string), profile["session"].(string), name, mime)
		if err != nil {
			actualDownloads = append(actualDownloads, map[string]any{"error": err.Error()})
			continue
		}
		stored, err := os.ReadFile(attachment.Path)
		if err != nil {
			t.Fatal(err)
		}
		actualDownloads = append(actualDownloads, map[string]any{"kind": attachment.Kind, "name": attachment.Name, "path": strings.TrimPrefix(attachment.Path, store.Root), "size": attachment.Size, "mimeType": attachment.MIME, "buffer": base64.StdEncoding.EncodeToString(attachment.Buffer), "stored": base64.StdEncoding.EncodeToString(stored), "check": CheckAttachmentLimit(attachment.Kind, attachment.Size, attachment.MIME)})
	}
	watcher := &ImageWatcher{}
	images := []any{}
	for _, chunk := range chunks {
		images = append(images, watcher.Feed(NewText(chunk)))
	}
	work := filepath.Join(home, "work")
	if err = os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(work, "inside.png"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "outside.png"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(home, "outside.png"), filepath.Join(work, "escape.png")); err != nil {
		t.Fatal(err)
	}
	safe := []any{}
	for _, profile := range outbound {
		source := profile["source"].(UploadSource)
		source.Path = strings.ReplaceAll(strings.ReplaceAll(source.Path, "$work", work), "$home", home)
		dir := work
		if profile["noWork"] == true {
			dir = ""
		}
		result := LoadSafeOutboundImage(PendingUpload{ID: "private", Source: source}, dir)
		if result.OK {
			safe = append(safe, map[string]any{"ok": true, "mime": result.MIME, "buffer": base64.StdEncoding.EncodeToString(result.Buffer)})
		} else {
			safe = append(safe, map[string]any{"ok": false, "reason": result.Reason})
		}
	}
	checks := []AttachmentLimit{}
	for _, profile := range limits {
		mime, _ := profile["mime"].(string)
		checks = append(checks, CheckAttachmentLimit(profile["kind"].(string), int64(profile["size"].(int)), mime))
	}
	if err = api.SendPhoto(context.Background(), "42", data, "private 😀 caption"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := raw(map[string]any{"downloads": actualDownloads, "calls": calls, "images": images, "drain": watcher.Drain(), "safe": safe, "limits": checks})
	mu.Unlock()
	exactJSON(t, got, want, "media")
	// The downloaded files are usable by the shared CLI attachment consumer.
	entry := AttachmentRef(LocalAttachment{Kind: "file", Name: "a.txt", Path: filepath.Join(store.Root, "telegram", "chat-42", "a.txt"), MIME: "application/octet-stream"})
	file, err := os.Open(entry.String("path"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil || !strings.EqualFold(base64.StdEncoding.EncodeToString(bytes), encoded) {
		t.Fatal("actual staged file not readable", err)
	}
}
