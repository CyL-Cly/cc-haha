package publicaccess

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type observingSocket struct {
	mu     sync.Mutex
	code   uint16
	reason string
}

func (s *observingSocket) CloseRemote(code uint16, reason string) {
	s.mu.Lock()
	s.code = code
	s.reason = reason
	s.mu.Unlock()
}
func (s *observingSocket) closed() string { s.mu.Lock(); defer s.mu.Unlock(); return s.reason }

func TestDeviceIdentityExpiryMessageRateAndSocketLimits(t *testing.T) {
	var clock atomic.Int64
	clock.Store(1000)
	path := filepath.Join(t.TempDir(), "devices.json")
	store := `{"version":1,"future":{"kept":true},"devices":[{"id":"","name":"legacy empty id","createdAt":1,"expiresAt":2000,"tokenHash":"` + hash("fixture-cookie") + `"},{"id":"raw\ud800","name":"raw units","createdAt":1,"expiresAt":3000,"tokenHash":"` + hash("second-cookie") + `"},{"id":"raw\ud801","name":"distinct raw units","createdAt":1,"expiresAt":3000,"tokenHash":"` + hash("third-cookie") + `"}]}`
	if e := os.WriteFile(path, []byte(store), 0600); e != nil {
		t.Fatal(e)
	}
	s := New(Dependencies{StorePath: path, Now: clock.Load})
	defer s.Disable()
	s.mu.Lock()
	e := s.loadLocked()
	if e == nil {
		e = s.enableLocked(raw("https://fixture.invalid"))
	}
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "http://localhost/api/public-access/session", nil)
	r.Header.Set("Cookie", CookieName+"=fixture-cookie")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	equalJSON(t, "valid empty-id device", w.Body.Bytes(), []byte(`{"authenticated":true}`))
	sockets := []*observingSocket{}
	for i := 0; i < 9; i++ {
		socket := &observingSocket{}
		accepted := s.RegisterSocket(`""`, socket)
		if accepted != (i < 8) {
			t.Fatalf("socket%d accepted%v", i, accepted)
		}
		sockets = append(sockets, socket)
	}
	if sockets[8].closed() != "Remote connection limit" {
		t.Fatal(sockets[8].closed())
	}
	for i := 0; i < 601; i++ {
		accepted := s.CheckMessage(`""`, sockets[0])
		if accepted != (i < 600) {
			t.Fatalf("message%d accepted%v", i, accepted)
		}
	}
	if sockets[0].closed() != "Remote rate limit" {
		t.Fatal(sockets[0].closed())
	}
	clock.Store(2000)
	if s.CheckMessage(`""`, sockets[1]) || sockets[1].closed() != "Remote access revoked" {
		t.Fatal("expired device passed to business")
	}
	a, b := &observingSocket{}, &observingSocket{}
	if !s.RegisterSocket(`"raw\ud800"`, a) || !s.RegisterSocket(`"raw\ud801"`, b) {
		t.Fatal("distinct UTF16 identities rejected")
	}
	s.deps.Authorization.LocalToken = "fixture"
	request := httptest.NewRequest("POST", "http://localhost/api/public-access/revoke", strings.NewReader(`{"id":"raw\ud800"}`))
	request.Header.Set("Authorization", "Bearer fixture")
	response := httptest.NewRecorder()
	s.Control(response, request)
	if response.Code != 200 || a.closed() != "Remote access revoked" || b.closed() != "" {
		t.Fatal("revocation confused raw UTF16 identities", response.Code, a.closed(), b.closed())
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte(`"future":{"kept":true}`)) || bytes.Contains(data, []byte(`"id":"raw\ud800"`)) || !bytes.Contains(data, []byte(`"id":"raw\ud801"`)) {
		t.Fatal("migration or revoke lost extensions/identity", string(data))
	}
	s.UnregisterSocket(b)
	s.mu.Lock()
	if e = s.originLocked(raw("https://changed.invalid")); e != nil {
		t.Fatal(e)
	}
	s.mu.Unlock()
	s.Disable()
}

func TestRemoteStaticNoStoreAndUnauthorizedBoundaries(t *testing.T) {
	s := New(Dependencies{StorePath: filepath.Join(t.TempDir(), "devices.json"), Static: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("actual fixture static"))
	})})
	defer s.Disable()
	s.mu.Lock()
	e := s.loadLocked()
	if e == nil {
		e = s.enableLocked(raw("https://fixture.invalid"))
	}
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/remote", "/assets/fixture.js"} {
		r := httptest.NewRequest("GET", "http://localhost"+path, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatal(w.Code, w.Header())
		}
	}
	r := httptest.NewRequest("GET", "http://localhost/api/public-access/session", nil)
	r.Header.Add("Origin", "https://fixture.invalid")
	r.Header.Add("Origin", "https://attacker.invalid")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("duplicate origin inherited first-header trust", w.Code)
	}
}
