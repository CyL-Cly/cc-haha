package imruntime

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestWecomOriginalSDKPhysicalHTTPSDecryptMIMEAndAtomicStaging(t *testing.T) {
	plain := append([]byte{0, 255, 1, 2}, []byte("中文 physical\x00 bytes")...)
	key := bytes.Repeat([]byte{17}, 32)
	pad := 32 - len(plain)%32
	padded := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(key)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key[:16]).CryptBlocks(encrypted, padded)
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	_, _ = zip.Write(encrypted)
	_ = zip.Close()
	var mu sync.Mutex
	calls := []protocol.JSONObject{}
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers := wecomJSONObject()
		for _, key := range []string{"Accept", "Content-Type", "User-Agent", "Accept-Encoding"} {
			headers.Set(strings.ToLower(key), raw(r.Header.Get(key)))
		}
		calls = append(calls, wecomJSONObject(wecomField("path", r.URL.RequestURI()), wecomField("method", r.Method), wecomField("headers", headers)))
		mu.Unlock()
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", "/plain")
			w.WriteHeader(302)
		case "/plain":
			w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''%E4%B8%AD%E6%96%87.PNG; filename=ignored.pdf`)
			_, _ = w.Write(encrypted)
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Disposition", `attachment; filename="encoded%20name.txt"`)
			_, _ = w.Write(compressed.Bytes())
		case "/raw":
			_, _ = w.Write(plain)
		case "/fail":
			w.WriteHeader(403)
			_, _ = w.Write([]byte("denied"))
		case "/bad-name":
			w.Header().Set("Content-Disposition", "filename=%FF.bin")
			_, _ = w.Write(plain)
		}
	}))
	defer peer.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}))
	candidates := []protocol.JSONObject{}
	for _, p := range []string{"/redirect", "/gzip", "/raw", "/fail", "/bad-name"} {
		c := wecomJSONObject(wecomField("kind", "file"), wecomField("name", "fallback.bin"), wecomField("url", peer.URL+p))
		if p == "/redirect" || p == "/gzip" {
			c.Set("aesKey", raw(base64.StdEncoding.EncodeToString(key)))
		}
		candidates = append(candidates, c)
	}
	sourceStore := t.TempDir()
	want := runWecomSource(t, map[string]any{"downloads": candidates, "ca": ca, "store": sourceStore})
	mu.Lock()
	sourceCalls := append([]protocol.JSONObject{}, calls...)
	calls = nil
	mu.Unlock()
	goStore := t.TempDir()
	store := NewAttachmentStore(goStore)
	store.Root = goStore
	client, e := NewWecomClient(context.Background(), WecomClientOptions{HTTP: peer.Client()})
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	results := []protocol.JSONObject{}
	for _, candidate := range candidates {
		got, e := client.DownloadCandidate(context.Background(), store, candidate, "private-session")
		var value any
		if e == nil {
			stored, e := os.ReadFile(got.Path)
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(stored, plain) || !bytes.Equal(got.Buffer, plain) {
				t.Fatal("staged/decrypted physical bytes differ")
			}
			if entries, e := filepath.Glob(got.Path + "*.part"); e != nil || len(entries) != 0 {
				t.Fatal("atomic staging left partial files")
			}
			value = map[string]any{"kind": got.Kind, "name": got.Name, "path": strings.TrimPrefix(got.Path, goStore), "buffer": base64.StdEncoding.EncodeToString(got.Buffer), "size": got.Size, "mimeType": got.MIME}
		}
		results = append(results, wecomCaptured(value, e))
	}
	exactJSON(t, raw(results), want.Get("downloads"), "physical download")
	mu.Lock()
	goCalls := append([]protocol.JSONObject{}, calls...)
	mu.Unlock()
	exactJSON(t, raw(goCalls), raw(sourceCalls), "physical HTTP wire")
}
