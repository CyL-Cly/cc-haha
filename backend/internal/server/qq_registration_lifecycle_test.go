package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/imregistration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type qqRenameGate struct {
	storage.FileSystem
	entered, release chan struct{}
	once             sync.Once
}

func (f *qqRenameGate) Rename(from, to string) error {
	f.once.Do(func() { close(f.entered) })
	<-f.release
	return f.FileSystem.Rename(from, to)
}

func TestQQConsumerRendererDisconnectRetainsAlreadySubmittedAtomicCommit(t *testing.T) {
	private := t.TempDir()
	file := filepath.Join(private, "adapters.json")
	seed := []byte(`{"future":17,"qq":{"appId":"old","appSecret":"old-secret","allowedUsers":["old"],"pairedUsers":[{"id":"old"}],"workspace":"keep"}}`)
	if e := os.WriteFile(file, seed, 0600); e != nil {
		t.Fatal(e)
	}
	files := &qqRenameGate{FileSystem: storage.New(storage.Options{}).FileSystem(), entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(files.release) })
	adapters, e := adapterconfig.NewService(adapterconfig.ServiceOptions{ConfigDir: private, Store: storage.New(storage.Options{FS: files}), LogError: func(error) {}})
	if e != nil {
		t.Fatal(e)
	}
	var keyMu sync.Mutex
	var bindKey []byte
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		if r.URL.Path == "/lite/create_bind_task" {
			key, e := base64.StdEncoding.DecodeString(body["key"].(string))
			if e != nil {
				t.Error(e)
			}
			keyMu.Lock()
			bindKey = key
			keyMu.Unlock()
			io.WriteString(w, `{"retcode":0,"data":{"task_id":"late-task"}}`)
			return
		}
		keyMu.Lock()
		key := append([]byte(nil), bindKey...)
		keyMu.Unlock()
		block, _ := aes.NewCipher(key)
		gcm, _ := cipher.NewGCM(block)
		nonce := bytes.Repeat([]byte{9}, 12)
		encrypted := append(append([]byte(nil), nonce...), gcm.Seal(nil, nonce, []byte("new-secret"), nil)...)
		json.NewEncoder(w).Encode(map[string]any{"retcode": 0, "data": map[string]any{"status": 2, "bot_appid": "new-id", "bot_encrypt_secret": base64.StdEncoding.EncodeToString(encrypted)}})
	}))
	defer upstream.Close()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	address := strings.TrimPrefix(upstream.URL, "https://")
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "example.com"}, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != "q.qq.com:443" {
			return nil, fmt.Errorf("unowned QQlogicaltarget")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	service, e := imregistration.NewQQ(imregistration.QQOptions{Client: &http.Client{Transport: transport}})
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close(context.Background())
	start, e := service.Start(context.Background(), protocol.NewJSONObject())
	if e != nil {
		t.Fatal(e)
	}
	// Retry real HTTP polls until the connector terminal result enters the actual
	// atomic writer. No connector state or business response is injected.
	s := &Server{ctx: context.Background()}
	handlerFailure := make(chan error, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e := s.qqRegistrationHTTP(w, r, []string{"login", "poll"}, service, adapters, func(e error) error { return e }); e != nil {
			select {
			case handlerFailure <- e:
			default:
			}
			w.WriteHeader(500)
		}
	}))
	defer peer.Close()
	input, _ := json.Marshal(map[string]json.RawMessage{"sessionKey": start.Get("sessionKey")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", peer.URL, bytes.NewReader(input))
	clientDone := make(chan error, 1)
	go func() {
		for {
			res, e := peer.Client().Do(request)
			if e != nil {
				clientDone <- e
				return
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			select {
			case <-files.entered:
				clientDone <- nil
				return
			default:
			}
			time.Sleep(time.Millisecond)
		}
	}()
	select {
	case <-files.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("real atomicwriter not entered")
	}
	cancel()
	select {
	case e := <-clientDone:
		if e == nil {
			t.Fatal("renderer must abort pending response")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("renderer did not settle")
	}
	if e := service.CancelJSON(start.Get("sessionKey")); e != nil {
		t.Fatal(e)
	}
	releaseOnce.Do(func() { close(files.release) })
	deadline := time.After(3 * time.Second)
	for {
		raw, e := os.ReadFile(file)
		if e == nil && strings.Contains(string(raw), `"new-secret"`) {
			var value protocol.JSONObject
			_ = value.UnmarshalJSON(raw)
			var section protocol.JSONObject
			_ = section.UnmarshalJSON(value.Get("qq"))
			if section.String("appId") != "new-id" || section.String("workspace") != "keep" || string(section.Get("allowedUsers")) != "[]" || string(section.Get("pairedUsers")) != "[]" {
				t.Fatal("real latecommit fields", string(raw))
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("renderer cancellation discarded submitted commit")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case e := <-handlerFailure:
		t.Fatal("actual handler failure", e)
	default:
	}

}
