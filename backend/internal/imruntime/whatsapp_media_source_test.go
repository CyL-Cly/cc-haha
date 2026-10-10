package imruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type whatsAppMediaRoute struct{ peer *httptest.Server }

func (r whatsAppMediaRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = "http"
	u.Host = r.peer.Listener.Addr().String()
	copy.URL = &u
	copy.Header.Set("X-Fixture-Logical-URL", req.URL.String())
	return r.peer.Client().Transport.RoundTrip(copy)
}
func TestWhatsAppActualSourceMediaKDFEncryptedDownloadFilenameAndOutbound(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	encoded := base64.StdEncoding.EncodeToString(key)
	plain := []byte("private encrypted media fixture\x00\xff")
	var mu sync.Mutex
	calls := []protocol.JSONObject{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logical := r.Header.Get("X-Fixture-Logical-URL")
		mu.Lock()
		calls = append(calls, protocol.NewJSONObject(protocol.JSONField{Name: "url", Value: raw(logical)}, protocol.JSONField{Name: "method", Value: raw(r.Method)}, protocol.JSONField{Name: "origin", Value: raw(r.Header.Get("Origin"))}))
		mu.Unlock()
		if strings.Contains(logical, "expired") {
			http.Error(w, "private", 410)
			return
		}
		mediaType := r.URL.Query().Get("type")
		if mediaType == "" {
			mediaType = "image"
		}
		cipher, e := EncryptWhatsAppMedia(plain, key, mediaType)
		if e != nil {
			t.Error(e)
		}
		w.Write(cipher)
	}))
	defer peer.Close()
	actions := []protocol.JSONObject{}
	f := func(n string, v any) protocol.JSONField { return protocol.JSONField{Name: n, Value: raw(v)} }
	add := func(op string, fields ...protocol.JSONField) {
		actions = append(actions, protocol.NewJSONObject(append([]protocol.JSONField{f("op", op)}, fields...)...))
	}
	for _, typ := range []string{"image", "audio", "video", "document", "sticker", "gif", "ptt", "thumbnail-link", "md-app-state", "ppic", "unknown"} {
		add("keys", f("key", encoded), f("type", typ))
	}
	for _, spec := range []struct{ kind, mime, name string }{{"image", "image/png", ""}, {"video", "video/mp4", ""}, {"document", "application/pdf", "../private-doc.pdf"}, {"audio", "audio/ogg; codecs=opus", ""}, {"sticker", "image/webp", ""}, {"document", "", ""}} {
		media := map[string]any{"url": "https://mmg.whatsapp.net/media?type=" + spec.kind, "mediaKey": encoded}
		if spec.mime != "" {
			media["mimetype"] = spec.mime
		}
		if spec.name != "" {
			media["fileName"] = spec.name
		}
		content := map[string]any{spec.kind + "Message": media}
		add("download", f("message", map[string]any{"message": content}))
	}
	add("download", f("message", map[string]any{"message": map[string]any{"conversation": "no media"}}))
	add("download", f("message", map[string]any{"message": map[string]any{"imageMessage": map[string]any{"url": "https://evil.private/never", "directPath": "/fallback?type=image", "mediaKey": encoded}}}))
	add("download", f("message", map[string]any{"message": map[string]any{"imageMessage": map[string]any{"url": "https://mmg.whatsapp.net/expired", "mediaKey": encoded}}}))
	for _, mime := range []string{"image/png", "video/mp4", "audio/ogg", "audio/mpeg", "application/pdf", ""} {
		add("send", f("jid", "1@s.whatsapp.net"), f("data", base64.StdEncoding.EncodeToString(plain)), f("mime", mime), f("caption", "private caption"), f("fileName", "private-file"))
	}
	want := object(whatsAppOriginal(t, "native-whatsapp-media.fixture.ts", map[string]any{"peerURL": peer.URL, "actions": actions}))
	mu.Lock()
	sourceCalls := append([]protocol.JSONObject{}, calls...)
	calls = nil
	mu.Unlock()
	home := t.TempDir()
	media := WhatsAppMedia{Store: NewAttachmentStore(home), HTTP: &http.Client{Transport: whatsAppMediaRoute{peer}}}
	values := []protocol.JSONObject{}
	sent := []protocol.JSONObject{}
	for _, a := range actions {
		var value any
		var e error
		switch a.String("op") {
		case "keys":
			var keys WhatsAppMediaKeys
			keys, e = WhatsAppDeriveMediaKeys(nodeBase64(a.String("key")), a.String("type"))
			if e == nil {
				value = protocol.NewJSONObject(f("info", WhatsAppHKDFInfo(a.String("type"))), f("iv", base64.StdEncoding.EncodeToString(keys.IV)), f("cipherKey", base64.StdEncoding.EncodeToString(keys.CipherKey)), f("macKey", base64.StdEncoding.EncodeToString(keys.MACKey)))
			}
		case "download":
			var local *LocalAttachment
			local, e = media.Download(context.Background(), object(a.Get("message")), "private-session")
			if local != nil {
				data, _ := os.ReadFile(local.Path)
				rel, _ := filepath.Rel(home, local.Path)
				value = protocol.NewJSONObject(f("kind", local.Kind), f("name", local.Name), f("path", rel), f("size", local.Size), f("mimeType", local.MIME), f("data", base64.StdEncoding.EncodeToString(local.Buffer)), f("disk", base64.StdEncoding.EncodeToString(data)))
			}
		case "send":
			payload := WhatsAppOutboundMedia(nodeBase64(a.String("data")), a.String("mime"), a.Get("caption"), a.Get("fileName"))
			sent = append(sent, protocol.NewJSONObject(protocol.JSONField{Name: "jid", Value: a.Get("jid")}, f("payload", payload)))
			continue
		}
		out := protocol.NewJSONObject(f("value", value))
		if e != nil {
			out = protocol.NewJSONObject(f("error", e.Error()))
		}
		values = append(values, out)
	}
	mu.Lock()
	goCalls := append([]protocol.JSONObject{}, calls...)
	mu.Unlock()
	if !bytes.Equal(raw(goCalls), raw(sourceCalls)) {
		t.Fatalf("calls Go%s Source%s", raw(goCalls), raw(sourceCalls))
	}
	got := protocol.NewJSONObject(f("values", values), f("calls", goCalls), f("sent", sent))
	var gv, wv any
	_ = json.Unmarshal(raw(got), &gv)
	_ = json.Unmarshal(raw(want), &wv)
	if !bytes.Equal(raw(gv), raw(wv)) {
		t.Fatalf("Go%s\nSource%s", raw(got), raw(want))
	}
	t.Logf("%d actual Source media profiles", len(actions))
}
func TestWhatsAppMediaCanceledDownloadClosesPhysicalRequest(t *testing.T) {
	entered := make(chan struct{})
	closed := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(closed) }))
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := WhatsAppMedia{Store: NewAttachmentStore(t.TempDir()), HTTP: &http.Client{Transport: whatsAppMediaRoute{peer}}}
	done := make(chan error, 1)
	go func() {
		_, e := m.Download(ctx, object(raw(map[string]any{"message": map[string]any{"imageMessage": map[string]any{"url": "https://mmg.whatsapp.net/private", "mediaKey": base64.StdEncoding.EncodeToString(make([]byte, 32))}}})), "private")
		done <- e
	}()
	<-entered
	cancel()
	if e := <-done; e == nil {
		t.Fatal("canceled media succeeded")
	}
	<-closed
}
