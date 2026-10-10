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
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/uipreferences"
)

type failingFS struct {
	storage.FileSystem
	failMkdir bool
	failRead  bool
}

func (boundary *failingFS) ReadFile(path string) ([]byte, error) {
	if boundary.failRead && filepath.Base(path) == "desktop-ui.json" {
		return nil, errors.New("fixture read denied")
	}
	return boundary.FileSystem.ReadFile(path)
}

func (boundary *failingFS) MkdirAll(path string, mode fs.FileMode) error {
	if boundary.failMkdir && filepath.Base(path) == "cc-haha" {
		return errors.New("fixture mkdir denied")
	}
	return boundary.FileSystem.MkdirAll(path, mode)
}

func httpFixture(t *testing.T) (*DesktopUI, string, *failingFS, *[]UnhandledError) {
	t.Helper()
	home := t.TempDir()
	boundary := &failingFS{FileSystem: storage.New(storage.Options{}).FileSystem()}
	store := storage.New(storage.Options{FS: boundary, Warn: func(string) {}})
	upgrader, err := migrations.New(migrations.Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	service, err := uipreferences.New(uipreferences.Options{Store: store, Upgrader: upgrader, ConfigDir: func() string { return home }, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
	if err != nil {
		t.Fatal(err)
	}
	events := []UnhandledError{}
	handler, err := NewDesktopUI(DesktopUIOptions{Service: service, Authorization: func() access.Authorization { return access.Authorization{PetToken: " fake-pet-token "} }, OnUnhandled: func(event UnhandledError) { events = append(events, event) }})
	if err != nil {
		t.Fatal(err)
	}
	return handler, home, boundary, &events
}

func call(handler *DesktopUI, method, path, body string, headers http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://fixture.invalid"+path, strings.NewReader(body))
	request.Header = headers
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertResponse(t *testing.T, response *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if response.Code != status || response.Body.String() != body {
		t.Fatalf("response = %d %s; want %d %s", response.Code, response.Body.String(), status, body)
	}
	if response.Header().Get("Content-Type") != "application/json;charset=utf-8" {
		t.Fatal("JSON content type differs from Response.json", response.Header())
	}
}

func TestDesktopUIRoutingMethodsAndWHATWGPathname(t *testing.T) {
	handler, _, _, events := httpFixture(t)
	for _, test := range []struct{ path, method, code, message string }{
		{"/api/desktop-ui", "GET", "NOT_FOUND", "Unknown desktop UI endpoint: undefined"},
		{"/api/desktop-ui/%70references", "GET", "NOT_FOUND", "Unknown desktop UI endpoint: %70references"},
		{"/api/desktop-ui/preferences/unknown", "GET", "NOT_FOUND", "Unknown desktop UI preferences endpoint: unknown"},
		{"/api/desktop-ui/preferences/profile/unknown", "PUT", "NOT_FOUND", "Unknown desktop UI profile endpoint: unknown"},
		{"/api/desktop-ui/preferences", "PUT", "METHOD_NOT_ALLOWED", "Method PUT not allowed"},
		{"/api/desktop-ui/preferences/sidebar", "GET", "METHOD_NOT_ALLOWED", "Method GET not allowed"},
		{"/api/desktop-ui/preferences/project-display-name", "GET", "METHOD_NOT_ALLOWED", "Method GET not allowed"},
		{"/api/desktop-ui/preferences/profile", "POST", "METHOD_NOT_ALLOWED", "Method POST not allowed"},
		{"/api/desktop-ui/preferences/pet", "DELETE", "METHOD_NOT_ALLOWED", "Method DELETE not allowed"},
		{"/api/desktop-ui/preferences/profile/avatar", "PATCH", "METHOD_NOT_ALLOWED", "Method PATCH not allowed"},
	} {
		status := 404
		if test.code == "METHOD_NOT_ALLOWED" {
			status = 405
		}
		assertResponse(t, call(handler, test.method, test.path, "", nil), status, `{"error":"`+test.code+`","message":"`+test.message+`"}`)
	}
	for _, path := range []string{"/api//desktop-ui/preferences/", "/api/desktop-ui/../desktop-ui/preferences", "/api/desktop-ui/%2e%2e/desktop-ui/preferences"} {
		response := call(handler, "GET", path, "", nil)
		if response.Code != 200 || !strings.HasPrefix(response.Body.String(), `{"preferences":{"schemaVersion":6`) || !strings.HasSuffix(response.Body.String(), `,"exists":false}`) {
			t.Fatal(path, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest("GET", "http://ignored.invalid/unrelated", nil)
	response := httptest.NewRecorder()
	handler.Handle(response, request, []string{"arbitrary", "resource", "preferences"})
	if response.Code != 200 || len(*events) != 0 {
		t.Fatal("handler ignored explicit segments", response.Code, events)
	}
}

func TestDesktopUIJSONDecodingValidationAndRealPersistence(t *testing.T) {
	handler, home, _, events := httpFixture(t)
	for _, body := range []string{"", "{bad", "{} {}", " \ufeff{}"} {
		assertResponse(t, call(handler, "PUT", "/api/desktop-ui/preferences/sidebar", body, nil), 400, `{"error":"BAD_REQUEST","message":"Invalid JSON body"}`)
	}
	for _, body := range []string{"null", "[]", "3", "true", `"text"`, "\ufeff{}"} {
		if response := call(handler, "PUT", "/api/desktop-ui/preferences/sidebar/ignored", body, nil); response.Code != 200 {
			t.Fatal(body, response.Code, response.Body.String())
		}
	}
	assertResponse(t, call(handler, "PUT", "/api/desktop-ui/preferences/profile", "null", nil), 400, `{"error":"BAD_REQUEST","message":"Profile preferences must be an object"}`)
	assertResponse(t, call(handler, "PUT", "/api/desktop-ui/preferences/project-display-name", "42", nil), 400, `{"error":"BAD_REQUEST","message":"Project display name update must be an object"}`)
	response := call(handler, "PUT", "/api/desktop-ui/preferences/project-display-name", `{"projectKey":"\ud800","displayName":"<>&\ud801"}`, nil)
	assertResponse(t, response, 200, `{"ok":true,"projectKey":"\ud800","displayName":"<>&\ud801"}`)
	response = call(handler, "PUT", "/api/desktop-ui/preferences/pet", `{"size":1e400,"future":9007199254740993,"overflow":1e400}`, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"size":144`) || !strings.Contains(response.Body.String(), `"future":9007199254740992,"overflow":null`) {
		t.Fatal(response.Code, response.Body.String())
	}
	saved, err := os.ReadFile(filepath.Join(home, "cc-haha", "desktop-ui.json"))
	if err != nil || !bytes.Contains(saved, []byte(`"future": 9007199254740992`)) || len(*events) != 0 {
		t.Fatal(string(saved), err, events)
	}
	request := httptest.NewRequest("PUT", "http://fixture.invalid/api/desktop-ui/preferences/sidebar", nil)
	request.Body = nil
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertResponse(t, response, 400, `{"error":"BAD_REQUEST","message":"Invalid JSON body"}`)
}

func TestPetProjectionUsesActualCredentialHelperAndFetchDuplicateHeaders(t *testing.T) {
	handler, _, _, _ := httpFixture(t)
	for _, test := range []struct {
		header []string
		pet    bool
	}{{nil, false}, {[]string{"Bearer fake-pet-token"}, true}, {[]string{"Bearer fake-pet-token ignored"}, true}, {[]string{"Bearer  fake-pet-token"}, false}, {[]string{"bearer fake-pet-token"}, false}, {[]string{"Bearer fake-pet-token", "Bearer wrong"}, false}} {
		headers := http.Header{"Authorization": test.header}
		response := call(handler, "PUT", "/api/desktop-ui/preferences/pet", `{"enabled":true}`, headers)
		if response.Code != 200 || strings.HasPrefix(response.Body.String(), `{"ok":true,"pet":`) != test.pet {
			t.Fatal(test, response.Code, response.Body.String())
		}
	}
	response := call(handler, "GET", "/api/desktop-ui/preferences/pet", "", nil)
	if !strings.HasPrefix(response.Body.String(), `{"exists":true,"pet":`) || strings.Contains(response.Body.String(), "sidebar") {
		t.Fatal(response.Body.String())
	}
	handler.options.Authorization = func() access.Authorization { return access.Authorization{} }
	if response = call(handler, "PUT", "/api/desktop-ui/preferences/pet", "{}", http.Header{"Authorization": {"Bearer fake-pet-token"}}); strings.HasPrefix(response.Body.String(), `{"ok":true,"pet":`) {
		t.Fatal("credential selector was cached")
	}
}

func TestAvatarRawBytesHeadersMissingAndDuplicateMIME(t *testing.T) {
	handler, home, _, events := httpFixture(t)
	path := "/api/desktop-ui/preferences/profile/avatar"
	assertResponse(t, call(handler, "GET", path, "", nil), 404, `{"error":"NOT_FOUND","message":"Profile avatar is not configured"}`)
	assertResponse(t, call(handler, "PUT", path, "bytes", http.Header{"Content-Type": {"image/png", "image/jpeg"}}), 400, `{"error":"BAD_REQUEST","message":"Unsupported avatar type"}`)
	image := string([]byte{0, 255, 1, 128})
	if response := call(handler, "PUT", path+"/ignored", image, http.Header{"Content-Type": {"image/png; charset=binary"}}); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	response := call(handler, "GET", path, "", nil)
	if response.Code != 200 || response.Body.String() != image || response.Header().Get("Content-Type") != "image/png" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(response.Code, response.Header(), response.Body.Bytes())
	}
	saved, err := os.ReadFile(filepath.Join(home, "cc-haha", "profile", "avatar.png"))
	if err != nil || string(saved) != image {
		t.Fatal(saved, err)
	}
	if response = call(handler, "DELETE", path, "", nil); response.Code != 200 || len(*events) != 0 {
		t.Fatal(response.Code, events)
	}
	assertResponse(t, call(handler, "GET", path, "", nil), 404, `{"error":"NOT_FOUND","message":"Profile avatar is not configured"}`)
	request := httptest.NewRequest("PUT", "http://fixture.invalid"+path, nil)
	request.Body = nil
	request.Header.Set("Content-Type", "image/webp")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal("empty arrayBuffer upload differs", response.Code, response.Body.String())
	}
}

type readFailure struct{ name, message string }

func (failure readFailure) Read([]byte) (int, error) { return 0, failure }
func (failure readFailure) Close() error             { return nil }
func (failure readFailure) Error() string            { return failure.message }
func (failure readFailure) ErrorName() string        { return failure.name }

func TestBodyReadErrorsAbortAndUnknownDiagnosticCallback(t *testing.T) {
	handler, _, boundary, events := httpFixture(t)
	for _, failure := range []readFailure{{"AbortError", "fixture cancelled"}, {"Error", "The operation was aborted"}, {"Error", "The connection was closed."}, {"Error", "fixture stream failed"}} {
		request := httptest.NewRequest("PUT", "http://fixture.invalid/api/desktop-ui/preferences/profile/avatar", nil)
		request.Body = failure
		request.Header.Set("Content-Type", "image/png")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if failure.message == "fixture stream failed" {
			assertResponse(t, response, 500, `{"error":"INTERNAL_ERROR","message":"An unexpected error occurred"}`)
			if len(*events) != 1 || (*events)[0].Type != "api_unhandled_error" || (*events)[0].Severity != "error" || (*events)[0].Summary != failure.message || (*events)[0].Error != failure {
				t.Fatal(events)
			}
		} else if response.Code != 499 || response.Body.Len() != 0 || len(response.Header()) != 0 {
			t.Fatal(response.Code, response.Body.String(), response.Header())
		}
		request = httptest.NewRequest("PUT", "http://fixture.invalid/api/desktop-ui/preferences/sidebar", nil)
		request.Body = failure
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		assertResponse(t, response, 400, `{"error":"BAD_REQUEST","message":"Invalid JSON body"}`)
	}
	boundary.failMkdir = true
	response := call(handler, "PUT", "/api/desktop-ui/preferences/sidebar", "{}", nil)
	assertResponse(t, response, 500, `{"error":"INTERNAL_ERROR","message":"An unexpected error occurred"}`)
	if len(*events) != 2 || (*events)[1].Summary != "fixture mkdir denied" {
		t.Fatal(events)
	}
}

func TestLoopbackServerUsesActualService(t *testing.T) {
	handler, _, _, _ := httpFixture(t)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	request, err := http.NewRequestWithContext(context.Background(), "PUT", server.URL+"/api/desktop-ui/preferences/project-display-name", strings.NewReader(`{"projectKey":"exact","displayName":"Loopback"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || string(body) != `{"ok":true,"projectKey":"exact","displayName":"Loopback"}` || response.Header.Get("Content-Type") != "application/json;charset=utf-8" {
		t.Fatal(response.StatusCode, response.Header, string(body))
	}
}

func TestRequiredDependenciesAndResponseEncodingErrors(t *testing.T) {
	for _, options := range []DesktopUIOptions{{}, {Authorization: func() access.Authorization { return access.Authorization{} }}, {OnUnhandled: func(UnhandledError) {}}} {
		if _, err := NewDesktopUI(options); err == nil {
			t.Fatal("missing production dependency accepted")
		}
	}
	if _, err := jsonResponse(200, make(chan int)); err == nil {
		t.Fatal("unsupported response value accepted")
	}
	handler, _, _, _ := httpFixture(t)
	response := httptest.NewRecorder()
	handler.errorResponse(&settings.APIError{StatusCode: 418, Message: "exact <>&"}).write(response)
	assertResponse(t, response, 418, `{"error":"ERROR","message":"exact <>&"}`)
	var body json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
}

func TestActualReadFailuresStayManagedAcrossRootPetAndAvatar(t *testing.T) {
	handler, home, boundary, events := httpFixture(t)
	if err := os.MkdirAll(filepath.Join(home, "cc-haha"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "cc-haha", "desktop-ui.json"), []byte(`{"schemaVersion":6}`), 0600); err != nil {
		t.Fatal(err)
	}
	boundary.failRead = true
	for _, path := range []string{"", "/pet", "/profile/avatar"} {
		response := call(handler, "GET", "/api/desktop-ui/preferences"+path, "", nil)
		if response.Code != 500 || !strings.Contains(response.Body.String(), `"error":"INTERNAL_ERROR"`) || !strings.Contains(response.Body.String(), "fixture read denied") {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	response := call(handler, "PUT", "/api/desktop-ui/preferences/pet", "{}", nil)
	if response.Code != 500 || len(*events) != 0 {
		t.Fatal("managed service read error became an unknown diagnostic event", response.Code, events)
	}
	assertResponse(t, call(handler, "PUT", "/api/desktop-ui/preferences/pet", "{bad", nil), 400, `{"error":"BAD_REQUEST","message":"Invalid JSON body"}`)
}

func TestMalformedAdapterURLIsExplicitUnhandledBoundary(t *testing.T) {
	handler, _, _, events := httpFixture(t)
	request := httptest.NewRequest("GET", "http://fixture.invalid/api/desktop-ui/preferences", nil)
	request.URL.Host = "%invalid"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertResponse(t, response, 500, `{"error":"INTERNAL_ERROR","message":"An unexpected error occurred"}`)
	if len(*events) != 1 {
		t.Fatal("adapter URL parse failure lacked required diagnostic callback")
	}
}
