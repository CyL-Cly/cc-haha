package publicaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
)

func TestMigrationPolicyAndControlMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	if e := os.MkdirAll(home, 0700); e != nil {
		t.Fatal(e)
	}
	migrations := []json.RawMessage{raw(nil), raw([]any{}), raw(42), raw(map[string]any{"version": 0, "legacyToken": "fake-lan-token", "future": map[string]any{"keep": true}}), raw(map[string]any{"version": 2}), json.RawMessage(`{"version":"2","devices":[{"id":"","name":"raw\ud800","createdAt":0,"expiresAt":1,"tokenHash":"` + strings.Repeat("a", 64) + `","future":7},{"id":1,"name":"bad","createdAt":0,"expiresAt":1,"tokenHash":"` + strings.Repeat("b", 64) + `"}]}`)}
	migrations = append(migrations, json.RawMessage(`{"version":1e309}`))
	paths := []map[string]string{}
	for _, path := range []string{"/", "/ws/fixture", "/ws/中文", "/ws/id/extra", "/api/sessions/id", "/api/conversations", "/api/providers", "/api/providers/settings", "/api/providers/auth-status", "/api/providers/id/activate", "/api/settings/user", "/api//settings/user/", "/api/models/current", "/api/permissions/mode", "/api/agents", "/api/tasks/extra", "/sdk/id", "/proxy/v1/messages", "/api/diagnostics", "/api/%73essions"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE", "HEAD", "OPTIONS"} {
			paths = append(paths, map[string]string{"path": path, "method": method})
		}
	}
	controls := []map[string]any{}
	add := func(path, method, body, token string) {
		header := map[string]string{}
		if token != "" {
			header["Authorization"] = token
		}
		controls = append(controls, map[string]any{"path": path, "method": method, "body": body, "headers": header})
	}
	for _, token := range []string{"", "bearer fixture-process-credential", "Bearer  fixture-process-credential", "Bearer wrong", "Bearer fixture-process-credential ignored"} {
		add("/api/public-access/status", "GET", "", token)
	}
	add("/api/public-access/status?token=fixture-process-credential", "GET", "", "")
	add("/api/public-access/unknown", "POST", "\ufeff{}", "Bearer fixture-process-credential")
	for _, row := range [][3]string{{"pairing", "POST", "{}"}, {"origin", "PUT", `{"publicUrl":"http://fixture.invalid"}`}, {"origin", "PUT", `{"publicUrl":"https://fixture.invalid/path"}`}, {"origin", "PUT", `{"publicUrl":"https://u:p@fixture.invalid"}`}, {"origin", "PUT", `{"publicUrl":"https://FIXTURE.invalid:443/"}`}, {"enable", "POST", "{}"}, {"status", "GET", ""}, {"pairing", "POST", "{}"}, {"approve", "POST", `{"id":"unknown"}`}, {"revoke", "POST", `{"id":"unknown"}`}, {"origin", "PUT", `{"publicUrl":"https://changed.invalid"}`}, {"enable", "POST", `{"publicUrl":12}`}, {"unknown", "POST", "{}"}, {"unknown", "POST", "[]"}, {"unknown", "POST", "null"}, {"unknown", "POST", "{broken"}, {"unknown", "POST", `{"pad":"` + strings.Repeat("🚀", 2048) + `"}`}, {"disable", "POST", "{}"}, {"status", "GET", ""}} {
		add("/api//public-access/"+row[0]+"/", row[1], row[2], "Bearer fixture-process-credential")
	}
	lifecycle := []map[string]any{
		{"local": true, "path": "/api/public-access/enable", "method": "POST", "body": map[string]any{"publicUrl": "https://fixture.invalid"}},
		{"path": "/api/public-access/session", "method": "GET"},
		{"path": "/api/sessions", "method": "GET", "headers": map[string]string{"Authorization": "Bearer fixture-process-credential"}},
		{"local": true, "path": "/api/public-access/pairing", "method": "POST"},
		{"path": "/api/public-access/pair", "method": "POST", "body": map[string]any{"secret": "$secret", "name": strings.Repeat("界", 79) + "😀\x00"}},
		{"path": "/api/public-access/pair", "method": "POST", "body": map[string]any{"secret": "$secret"}},
		{"path": "/api/public-access/claim", "method": "POST", "body": map[string]any{"id": "$id", "claimSecret": "$claimSecret"}},
		{"local": true, "path": "/api/public-access/status", "method": "GET"},
		{"local": true, "path": "/api/public-access/approve", "method": "POST", "body": map[string]any{"id": "$id"}},
		{"path": "/api/public-access/claim", "method": "POST", "body": map[string]any{"id": "$id", "claimSecret": "$claimSecret"}},
		{"path": "/api/public-access/session", "method": "GET", "cookie": true},
		{"path": "/ws/fixture", "method": "GET", "cookie": true},
		{"path": "/api/settings/user", "method": "GET", "cookie": true},
		{"path": "/api/providers/auth-status", "method": "GET", "cookie": true},
		{"path": "/api/diagnostics", "method": "GET", "cookie": true},
		{"path": "/api/sessions", "method": "POST", "cookie": true, "origin": nil},
		{"path": "/api/sessions", "method": "GET", "cookie": true, "headers": map[string]string{"Sec-Fetch-Site": "same-site"}},
		{"local": true, "path": "/api/public-access/disable", "method": "POST"},
		{"local": true, "path": "/api/public-access/enable", "method": "POST", "body": map[string]any{"publicUrl": "https://fixture.invalid"}},
		{"path": "/api/public-access/session", "method": "GET", "cookie": true},
		{"local": true, "path": "/api/public-access/revoke", "method": "POST", "body": map[string]any{"id": "$id"}},
		{"path": "/api/sessions", "method": "GET", "cookie": true},
		{"local": true, "path": "/api/public-access/pairing", "method": "POST"},
		{"path": "/api/public-access/pair", "method": "POST", "body": map[string]any{"secret": "$secret"}, "advance": 300001},
	}
	input := map[string]any{"migrations": migrations, "paths": paths, "controls": controls, "lifecycle": lifecycle}
	data, _ := json.Marshal(input)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/public-access.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_PUBLIC_ACCESS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source %v %.3500s", e, output)
	}
	var source map[string]json.RawMessage
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatalf("Source output %v %.2500s", e, output)
	}
	results := []any{}
	for _, value := range migrations {
		v, e := MigrateStore(value)
		if e != nil {
			results = append(results, map[string]any{"error": e.Error()})
		} else {
			results = append(results, map[string]any{"value": v})
		}
	}
	equalJSON(t, "migration", raw(results), source["migrations"])
	allowed := []bool{}
	for _, row := range paths {
		allowed = append(allowed, BusinessAllowed(parts(row["path"]), row["method"]))
	}
	equalJSON(t, "allow matrix", raw(allowed), source["paths"])
	s := New(Dependencies{Authorization: access.Authorization{LocalToken: "fixture-process-credential"}, StorePath: filepath.Join(home, "go-devices.json"), Now: func() int64 { return 1000 }, BusinessAllowed: BusinessAllowed})
	defer s.Disable()
	results = []any{}
	for _, row := range controls {
		request := httptest.NewRequest(row["method"].(string), "http://localhost"+row["path"].(string), strings.NewReader(row["body"].(string)))
		for key, value := range row["headers"].(map[string]string) {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		s.Control(response, request)
		var body map[string]any
		if e = json.Unmarshal(response.Body.Bytes(), &body); e != nil {
			t.Fatalf("Go body %v %s", e, response.Body.String())
		}
		if body["port"] != nil {
			body["port"] = "<actual-port>"
		}
		if body["secret"] != nil {
			body["secret"] = "<actual-secret>"
		}
		headers := map[string]string{}
		for key, value := range response.Header() {
			headers[strings.ToLower(key)] = strings.Join(value, ", ")
		}
		results = append(results, map[string]any{"status": response.Code, "body": body, "headers": headers})
	}
	equalJSON(t, "controls", raw(results), source["controls"])
	var clock atomic.Int64
	clock.Store(1000)
	s.deps.Now = clock.Load
	s.deps.Business = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"hasAuth": true, "source": "env", "env": map[string]string{"PRIVATE_KEY": "fake-secret"}, "language": "en", "autoQuestion": map[string]bool{"enabled": true}})
	})
	values := map[string]string{}
	results = []any{}
	for _, row := range lifecycle {
		if advance, ok := row["advance"].(int); ok {
			clock.Add(int64(advance))
		}
		body := raw(row["body"])
		if row["body"] == nil {
			body = []byte("{}")
		}
		for key, value := range values {
			body = bytes.ReplaceAll(body, []byte("$"+key), []byte(value))
		}
		var status int
		var headers http.Header
		var data []byte
		if row["local"] == true {
			r := httptest.NewRequest(row["method"].(string), "http://localhost"+row["path"].(string), bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer fixture-process-credential")
			w := httptest.NewRecorder()
			s.Control(w, r)
			status = w.Code
			headers = w.Header()
			data = w.Body.Bytes()
		} else {
			s.mu.Lock()
			st := s.statusLocked()
			port := st["port"].(int)
			s.mu.Unlock()
			method := row["method"].(string)
			var reader io.Reader
			if method != "GET" && method != "HEAD" {
				reader = bytes.NewReader(body)
			}
			r, _ := http.NewRequest(method, "http://127.0.0.1:"+strconv.Itoa(port)+row["path"].(string), reader)
			origin, exists := row["origin"]
			if !exists {
				origin = "https://fixture.invalid"
			}
			if origin != nil {
				r.Header.Set("Origin", origin.(string))
			}
			if row["cookie"] == true {
				r.Header.Set("Cookie", values["cookie"])
			}
			if extra, ok := row["headers"].(map[string]string); ok {
				for key, value := range extra {
					r.Header.Set(key, value)
				}
			}
			response, e := http.DefaultClient.Do(r)
			if e != nil {
				t.Fatal(e)
			}
			status = response.StatusCode
			headers = response.Header
			data, _ = io.ReadAll(response.Body)
			_ = response.Body.Close()
		}
		if cookie := headers.Get("Set-Cookie"); cookie != "" {
			values["cookie"] = strings.Split(cookie, ";")[0]
		}
		value := object(data)
		if value.String("secret") != "" {
			values["secret"] = value.String("secret")
			value.Set("secret", raw("<actual-secret>"))
		}
		if value.String("claimSecret") != "" {
			values["id"] = value.String("id")
			values["claimSecret"] = value.String("claimSecret")
			value.Set("id", raw("<actual-id>"))
			value.Set("claimSecret", raw("<actual-claim>"))
		}
		if string(value.Get("port")) != "null" && len(value.Get("port")) > 0 {
			value.Set("port", raw("<actual-port>"))
		}
		for _, key := range []string{"pending", "devices"} {
			var rows []json.RawMessage
			if json.Unmarshal(value.Get(key), &rows) == nil && rows != nil {
				normalized := []any{}
				for _, device := range rows {
					v := object(device)
					v.Set("id", raw("<actual-id>"))
					normalized = append(normalized, v)
				}
				value.Set(key, raw(normalized))
			}
		}
		projected := map[string]string{}
		for key, rows := range headers {
			key = strings.ToLower(key)
			if key == "date" || key == "content-length" || key == "transfer-encoding" || key == "connection" || key == "keep-alive" {
				continue
			}
			v := strings.Join(rows, ", ")
			if key == "set-cookie" {
				v = CookieName + "=<actual-cookie>" + v[strings.Index(v, ";"):]
			}
			projected[key] = v
		}
		results = append(results, map[string]any{"status": status, "body": value, "headers": projected})
	}
	equalJSON(t, "real listener lifecycle", raw(results), source["lifecycle"])
}
func equalJSON(t *testing.T, label string, got, want []byte) {
	t.Helper()
	var a, b any
	if e := json.Unmarshal(got, &a); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(want, &b); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(a, b) {
		if rows, ok := a.([]any); ok {
			if original, ok := b.([]any); ok && len(rows) == len(original) {
				for i, row := range rows {
					if !reflect.DeepEqual(row, original[i]) {
						t.Errorf("%s profile%d Go=%s Source=%s", label, i, raw(row), raw(original[i]))
					}
				}
				return
			}
		}
		t.Errorf("%s Go=%s Source=%s", label, got, want)
	}
}

func TestActualListenerPairApproveRevokePersistence(t *testing.T) {
	store := filepath.Join(t.TempDir(), "devices.json")
	s := New(Dependencies{Authorization: access.Authorization{LocalToken: "fixture"}, StorePath: store, BusinessAllowed: BusinessAllowed, Business: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"language": "en", "autoQuestion": map[string]any{"enabled": true}, "env": map[string]string{"PRIVATE": "fake-secret"}, "hasAuth": true, "source": "env"})
	})})
	defer s.Disable()
	control := func(route string, body any) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/api/public-access/"+route, bytes.NewReader(raw(body)))
		r.Header.Set("Authorization", "Bearer fixture")
		w := httptest.NewRecorder()
		s.Control(w, r)
		return w
	}
	if w := control("enable", map[string]string{"publicUrl": "https://fixture.invalid"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	s.mu.Lock()
	base := "http://" + s.listener.Addr().String()
	s.mu.Unlock()
	remote := func(path string, body any, cookie string) *http.Response {
		method := "GET"
		var reader io.Reader
		if body != nil {
			method = "POST"
			reader = bytes.NewReader(raw(body))
		}
		r, _ := http.NewRequest(method, base+path, reader)
		r.Header.Set("Origin", "https://fixture.invalid")
		r.Header.Set("Cookie", cookie)
		response, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = response.Body.Close() })
		return response
	}
	code := object(control("pairing", map[string]any{}).Body.Bytes()).String("secret")
	response := remote("/api/public-access/pair", map[string]string{"secret": code, "name": "phone"}, "")
	data, _ := io.ReadAll(response.Body)
	pair := object(data)
	if response.StatusCode != 200 {
		t.Fatal(string(data))
	}
	if r := remote("/api/public-access/pair", map[string]string{"secret": code}, ""); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	claim := map[string]string{"id": pair.String("id"), "claimSecret": pair.String("claimSecret")}
	r := remote("/api/public-access/claim", claim, "")
	data, _ = io.ReadAll(r.Body)
	equalJSON(t, "pending", data, []byte(`{"status":"pending"}`))
	control("approve", map[string]string{"id": pair.String("id")})
	r = remote("/api/public-access/claim", claim, "")
	cookie := strings.Split(r.Header.Get("Set-Cookie"), ";")[0]
	if cookie == "" {
		t.Fatal("no approved cookie")
	}
	stored, _ := os.ReadFile(store)
	if bytes.Contains(stored, []byte(strings.TrimPrefix(cookie, CookieName+"="))) {
		t.Fatal("raw cookie leaked to store")
	}
	r = remote("/api/settings/user", nil, cookie)
	data, _ = io.ReadAll(r.Body)
	equalJSON(t, "remote settings", data, []byte(`{"language":"en"}`))
	r = remote("/api/diagnostics", nil, cookie)
	if r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	s.Disable()
	control("enable", map[string]string{"publicUrl": "https://fixture.invalid"})
	s.mu.Lock()
	base = "http://" + s.listener.Addr().String()
	s.mu.Unlock()
	if r = remote("/api/public-access/session", nil, cookie); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	data, _ = io.ReadAll(r.Body)
	equalJSON(t, "restored", data, []byte(`{"authenticated":true}`))
	control("revoke", map[string]string{"id": pair.String("id")})
	if r = remote("/api/sessions", nil, cookie); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
}
