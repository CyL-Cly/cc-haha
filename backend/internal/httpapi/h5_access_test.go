package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/h5access"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type h5HTTPFileSystem struct {
	storage.FileSystem
	readHook        func() error
	failMkdir       bool
	failAfterCommit bool
	committed       bool
	unlinks         int
}

func (boundary *h5HTTPFileSystem) ReadFile(path string) ([]byte, error) {
	if filepath.Base(path) == "settings.json" && filepath.Base(filepath.Dir(path)) == "cc-haha" {
		if boundary.failAfterCommit && boundary.committed {
			return nil, errors.New("fixture cache read denied")
		}
		if boundary.readHook != nil {
			if err := boundary.readHook(); err != nil {
				return nil, err
			}
		}
	}
	return boundary.FileSystem.ReadFile(path)
}

func (boundary *h5HTTPFileSystem) MkdirAll(path string, mode fs.FileMode) error {
	if boundary.failMkdir {
		return errors.New("fixture mkdir denied")
	}
	return boundary.FileSystem.MkdirAll(path, mode)
}

func (boundary *h5HTTPFileSystem) Rename(from, to string) error {
	if err := boundary.FileSystem.Rename(from, to); err != nil {
		return err
	}
	if filepath.Base(to) == "settings.json" {
		boundary.committed = true
	}
	return nil
}

func (boundary *h5HTTPFileSystem) Remove(path string) error {
	boundary.unlinks++
	return boundary.FileSystem.Remove(path)
}

func h5HTTPFixture(t *testing.T) (*H5Access, string, *h5HTTPFileSystem, *[]UnhandledError) {
	t.Helper()
	home := t.TempDir()
	boundary := &h5HTTPFileSystem{FileSystem: storage.New(storage.Options{}).FileSystem()}
	store := storage.New(storage.Options{FS: boundary, Warn: func(string) {}})
	upgrader, err := migrations.New(migrations.Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := settings.NewManaged(settings.Options{Store: store, HomeDir: home, ConfigDir: filepath.Join(home, ".claude"), Environment: func() map[string]string { return map[string]string{} }, EnsureMigrated: upgrader.SettingsCallback})
	if err != nil {
		t.Fatal(err)
	}
	service, err := h5access.New(h5access.Options{Managed: managed, Environment: func() map[string]string { return map[string]string{} }, NetworkInterfaces: func() []h5access.NetworkInterface { return []h5access.NetworkInterface{} }, ServerPort: func() int { return 3456 }})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewH5GraceCache(service)
	if err != nil {
		t.Fatal(err)
	}
	events := []UnhandledError{}
	handler, err := NewH5Access(H5AccessOptions{Service: service, GraceCache: cache, OnUnhandled: func(event UnhandledError) { events = append(events, event) }})
	if err != nil {
		t.Fatal(err)
	}
	return handler, home, boundary, &events
}

func h5Call(handler *H5Access, method, path, body string, headers http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://fixture.invalid"+path, strings.NewReader(body))
	request.Header = headers
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func h5Seed(t *testing.T, home, raw string) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "cc-haha", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func h5ReadErrorBody(path, cause string) string {
	message, _ := json.Marshal("Failed to read cc-haha managed settings from " + path + ": Error: " + cause)
	return `{"error":"INTERNAL_ERROR","message":` + string(message) + `}`
}

func TestH5HTTPMethodErrorsRoutesAndSourcePathSegments(t *testing.T) {
	handler, _, _, events := h5HTTPFixture(t)
	for _, sub := range []string{"", "/enable", "/disable", "/regenerate", "/verify"} {
		method := "PATCH"
		response := h5Call(handler, method, "/api/h5-access"+sub, "", nil)
		assertResponse(t, response, 405, `{"error":"METHOD_NOT_ALLOWED","message":"Method PATCH not allowed on /api/h5-access`+sub+`"}`)
	}
	for _, sub := range []string{"%76erify", "unknown", "%e4%b8%ad"} {
		assertResponse(t, h5Call(handler, "GET", "/api/h5-access/"+sub, "", nil), 404, `{"error":"NOT_FOUND","message":"Unknown h5-access endpoint: `+sub+`"}`)
	}
	for _, path := range []string{"/api//h5-access", "/api/h5-access/", "/api/h5-access/../h5-access", "/api/h5-access/%2e%2e/h5-access"} {
		response := h5Call(handler, "GET", path, "", nil)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"diagnostics":`) {
			t.Fatalf("WHATWG source pathname changed: %s -> %d %s", path, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest("GET", "/ignored", nil)
	response := httptest.NewRecorder()
	handler.Handle(response, request, []string{})
	if response.Code != 200 {
		t.Fatal("missing segment should select root")
	}
	response = httptest.NewRecorder()
	handler.Handle(response, request, []string{"any", "resource", ""})
	assertResponse(t, response, 404, `{"error":"NOT_FOUND","message":"Unknown h5-access endpoint: "}`)
	if len(*events) != 0 {
		t.Fatal("handled API failures were diagnosed as unknown", *events)
	}
}

func TestH5HTTPActualTokenActionsIgnorePOSTBodiesAndRefreshOnlyPUT(t *testing.T) {
	handler, home, _, events := h5HTTPFixture(t)
	path := h5Seed(t, home, `{"env":{},"future":"keep","h5Access":{"disconnectGraceSeconds":42}}`)
	if handler.options.GraceCache.Milliseconds() != 30000 {
		t.Fatal("constructing handler eagerly refreshed source cache")
	}
	enable := h5Call(handler, "POST", "/api/h5-access/enable/ignored", "{bad", nil)
	var first h5access.EnableResult
	if err := json.Unmarshal(enable.Body.Bytes(), &first); err != nil || enable.Code != 200 || first.Token == "" {
		t.Fatalf("real enable failed: %s %v", enable.Body, err)
	}
	verify := func(token string) *httptest.ResponseRecorder {
		return h5Call(handler, "POST", "/api/h5-access/verify/extra", "{bad", http.Header{"Authorization": {"bEaReR\t" + token}})
	}
	assertResponse(t, verify(first.Token), 200, `{"ok":true}`)
	assertResponse(t, verify(first.Token+" ignored"), 401, `{"error":"UNAUTHORIZED","message":"Invalid or missing H5 access token"}`)
	assertResponse(t, h5Call(handler, "POST", "/api/h5-access/verify?token="+first.Token, "", nil), 401, `{"error":"UNAUTHORIZED","message":"Invalid or missing H5 access token"}`)
	assertResponse(t, h5Call(handler, "POST", "/api/h5-access/verify", "", http.Header{"Authorization": {"Bearer " + first.Token, "Bearer extra"}}), 401, `{"error":"UNAUTHORIZED","message":"Invalid or missing H5 access token"}`)
	if h5Call(handler, "POST", "/api/h5-access/disable", "42", nil).Code != 200 || verify(first.Token).Code != 401 {
		t.Fatal("disable did not revoke validation")
	}
	var second h5access.EnableResult
	_ = json.Unmarshal(h5Call(handler, "POST", "/api/h5-access/enable", "[]", nil).Body.Bytes(), &second)
	if first.Token != second.Token {
		t.Fatal("re-enabling changed the existing token")
	}
	var third h5access.EnableResult
	_ = json.Unmarshal(h5Call(handler, "POST", "/api/h5-access/regenerate", "null", nil).Body.Bytes(), &third)
	if first.Token == third.Token || verify(first.Token).Code != 401 || verify(third.Token).Code != 200 {
		t.Fatal("regeneration transition changed")
	}
	if handler.options.GraceCache.Milliseconds() != 30000 {
		t.Fatal("token action refreshed grace cache")
	}
	put := h5Call(handler, "PUT", "/api/h5-access", `{"disconnectGraceSeconds":8,"enabled":false,"token":"forged","allowedOrigins":["https://example.com/path"],"fixedPort":28670}`, nil)
	if put.Code != 200 || handler.options.GraceCache.Milliseconds() != 8000 {
		t.Fatalf("PUT didn't refresh actual cache: %d %s", put.Code, put.Body)
	}
	if verify(third.Token).Code != 200 {
		t.Fatal("unknown root fields bypassed the four-field projection")
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Contains(raw, []byte(`"future": "keep"`)) || len(*events) != 0 {
		t.Fatal("unknown root fields lost or normal actions diagnosed", string(raw), *events)
	}
}

func TestH5PUTFetchDecodingAndObjectShapeBeforeBusinessRead(t *testing.T) {
	handler, home, boundary, _ := h5HTTPFixture(t)
	path := h5Seed(t, home, `{"env":{},"committed":true}`)
	for _, body := range []string{"", "{bad", "null", "[]", "1", "false", `"string"`, " \ufeff{}", "{} {}"} {
		assertResponse(t, h5Call(handler, "PUT", "/api/h5-access", body, nil), 400, `{"error":"BAD_REQUEST","message":"Invalid JSON body"}`)
	}
	read := 0
	boundary.readHook = func() error { read++; return nil }
	for _, body := range []string{"\ufeff{}", "{\"unknown\":\"\xff\"}", `{"fixedPort":28670,"fixedPort":28671}`} {
		if response := h5Call(handler, "PUT", "/api/h5-access", body, nil); response.Code != 200 {
			t.Fatalf("Fetch valid/replaced UTF8 differs: %d %s", response.Code, response.Body)
		}
	}
	if read == 0 {
		t.Fatal("valid PUT bypassed real storage")
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Contains(raw, []byte(`"fixedPort": 28671`)) {
		t.Fatal("duplicate JSON field did not use last value", string(raw))
	}
	request := httptest.NewRequest("PUT", "/api/h5-access", nil)
	request.Body = readFailure{name: "AbortError", message: "fixture cancelled"}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertResponse(t, response, 400, `{"error":"BAD_REQUEST","message":"Invalid JSON body"}`)
	assertResponse(t, h5Call(handler, "PUT", "/api/h5-access", `{"fixedPort":1e400}`, nil), 400, `{"error":"BAD_REQUEST","message":"fixedPort must be a browser-safe integer between 1024 and 65535"}`)
}

func TestH5GETStartsBothRealReadsAndReturnsBeforeOtherFailedPromiseCompletes(t *testing.T) {
	handler, home, boundary, _ := h5HTTPFixture(t)
	path := h5Seed(t, home, `{"env":{}}`)
	handler.options.GraceCache.Refresh(context.Background()) // warm actual upgrader
	secondStarted, finishSecond, secondDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var count atomic.Int32
	boundary.readHook = func() error {
		if count.Add(1) == 1 {
			<-secondStarted
			return errors.New("fixture first read denied")
		}
		close(secondStarted)
		<-finishSecond
		close(secondDone)
		return nil
	}
	returned := make(chan *httptest.ResponseRecorder, 1)
	go func() { returned <- h5Call(handler, "GET", "/api/h5-access", "", nil) }()
	select {
	case response := <-returned:
		assertResponse(t, response, 500, h5ReadErrorBody(path, "fixture first read denied"))
	case <-time.After(5 * time.Second):
		close(finishSecond)
		t.Fatal("GET serialized reads or waited for the other failing Promise.all operation")
	}
	close(finishSecond)
	<-secondDone
}

func TestH5KnownReadAndUnknownMkdirErrorsPreserveTheirDifferentDiagnostics(t *testing.T) {
	handler, home, boundary, events := h5HTTPFixture(t)
	path := h5Seed(t, home, `{"env":{},"committed":true}`)
	handler.options.GraceCache.Refresh(context.Background())
	boundary.readHook = func() error { return errors.New("fixture read denied") }
	assertResponse(t, h5Call(handler, "PUT", "/api/h5-access", `{}`, nil), 500, h5ReadErrorBody(path, "fixture read denied"))
	for _, sub := range []string{"enable", "disable", "regenerate", "verify"} {
		assertResponse(t, h5Call(handler, "POST", "/api/h5-access/"+sub, "", http.Header{"Authorization": {"Bearer fixture-token"}}), 500, h5ReadErrorBody(path, "fixture read denied"))
	}
	if len(*events) != 0 {
		t.Fatal("typed read failure became unhandled", *events)
	}
	boundary.readHook = nil
	boundary.failMkdir = true
	assertResponse(t, h5Call(handler, "POST", "/api/h5-access/enable", "", nil), 500, `{"error":"INTERNAL_ERROR","message":"An unexpected error occurred"}`)
	if boundary.unlinks != 0 || len(*events) != 1 || (*events)[0].Summary != "fixture mkdir denied" || (*events)[0].Type != "api_unhandled_error" {
		t.Fatalf("mkdir outside catch changed cleanup or diagnostic: %d, %v", boundary.unlinks, *events)
	}
	if raw, _ := os.ReadFile(path); string(raw) != `{"env":{},"committed":true}` {
		t.Fatal("mkdir failure changed committed state", string(raw))
	}
}

func TestH5GraceCacheDefaultRefreshFailureAndConcurrentSyncAccess(t *testing.T) {
	handler, home, boundary, _ := h5HTTPFixture(t)
	h5Seed(t, home, `{"env":{},"h5Access":{"disconnectGraceSeconds":42}}`)
	cache := handler.options.GraceCache
	if cache.Milliseconds() != 30000 || cache.Refresh(context.Background()) != 42000 {
		t.Fatal("actual cache refresh/default mismatch")
	}
	boundary.failAfterCommit = true
	response := h5Call(handler, "PUT", "/api/h5-access", `{"disconnectGraceSeconds":8}`, nil)
	if response.Code != 200 || cache.Milliseconds() != 42000 {
		t.Fatalf("best-effort refresh failed the committed PUT: %d %d", response.Code, cache.Milliseconds())
	}
	boundary.failAfterCommit = false
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			cache.Refresh(context.Background())
			_ = cache.Milliseconds()
		}()
	}
	group.Wait()
	if cache.Milliseconds() != 8000 {
		t.Fatal("real shared cache didn't converge after recovered read")
	}
}

func TestH5BearerOwnJavaScriptWhitespaceAndEntireTokenCapture(t *testing.T) {
	for _, whitespace := range []string{" ", "  ", "\t", "\n", "\r", "\v", "\f", "\u00a0", "\u1680", "\u2000", "\u200a", "\u2028", "\u2029", "\u202f", "\u205f", "\u3000", "\ufeff"} {
		request := httptest.NewRequest("POST", "/", nil)
		request.Header.Set("Authorization", "bEaReR"+whitespace+"token ignored")
		if actual := h5BearerToken(request); actual != "token ignored" {
			t.Fatalf("handler regex whitespace %q = %q", whitespace, actual)
		}
	}
	for _, value := range []string{"", "Bearer", "Basic token", " Bearer token", "Bearer\u0085token", "Bearer token\nnext", "Bearer token\r", "Bearer token\u2028"} {
		request := httptest.NewRequest("POST", "/", nil)
		request.Header.Set("Authorization", value)
		if actual := h5BearerToken(request); actual != "" {
			t.Fatalf("handler regex widened %q = %q", value, actual)
		}
	}
}

func TestH5RequiredBootstrapAndKnownErrorPrecedence(t *testing.T) {
	handler, _, _, events := h5HTTPFixture(t)
	if _, err := NewH5GraceCache(nil); err == nil {
		t.Fatal("missing actual service accepted")
	}
	for _, options := range []H5AccessOptions{{}, {Service: handler.options.Service}, {Service: handler.options.Service, GraceCache: handler.options.GraceCache}} {
		if _, err := NewH5Access(options); err == nil {
			t.Fatal("missing shared cache/diagnostics accepted")
		}
	}
	other, _, _, _ := h5HTTPFixture(t)
	options := handler.options
	options.GraceCache = other.options.GraceCache
	if _, err := NewH5Access(options); err == nil {
		t.Fatal("cache from a different service accepted")
	}
	for _, err := range []error{readFailure{name: "AbortError", message: "fixture cancelled"}, errors.New("The operation was aborted"), errors.New("The connection was closed.")} {
		response := handler.errorResponse(err)
		if response.status != 499 || len(response.body) != 0 || len(response.headers) != 0 {
			t.Fatalf("named Fetch abort response changed: %#v", response)
		}
	}
	response := handler.errorResponse(&settings.APIError{StatusCode: 409, Message: "The operation was aborted"})
	if string(response.body) != `{"error":"ERROR","message":"The operation was aborted"}` || response.status != 409 {
		t.Fatal("ApiError priority/code fallback changed", response)
	}
	if _, err := handler.settingsResponse(h5access.PublicSettings{}, errors.New("fixture settings rejected")); err == nil {
		t.Fatal("settings response swallowed business failure")
	}
	response = handler.errorResponse(&storage.FileError{Operation: "read", Label: "unrelated", Path: "/temporary", Err: errors.New("cause")})
	if response.status != 500 || len(*events) != 1 {
		t.Fatal("unknown file error was falsely claimed as typed managed contract")
	}
	request := httptest.NewRequest("GET", "http://fixture.invalid/api/h5-access", nil)
	request.URL.Host = "%invalid"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != 500 || len(*events) != 2 {
		t.Fatal("malformed adapter URL error disappeared")
	}
}

func TestH5ActualLoopbackTLSResponseAndGraceUpdate(t *testing.T) {
	handler, _, _, _ := h5HTTPFixture(t)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/h5-access")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/json;charset=utf-8" || !bytes.Contains(body, []byte(`"diagnostics":`)) {
		t.Fatalf("loopback response failed: %s %v", body, err)
	}
	request, _ := http.NewRequest("PUT", server.URL+"/api//h5-access/", strings.NewReader(`{"disconnectGraceSeconds":5}`))
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || handler.options.GraceCache.Milliseconds() != 5000 {
		t.Fatal("real loopback PUT did not persist and update shared cache")
	}
}
