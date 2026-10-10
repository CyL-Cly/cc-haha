package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestConnectorHTTPAdmissionAndErrorsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	type request struct {
		Method string  `json:"method"`
		Path   string  `json:"path"`
		Body   *string `json:"body"`
	}
	requests := []request{{Method: "GET", Path: "/api/connectors"}, {Method: "GET", Path: "/api/connectors/sample"}, {Method: "GET", Path: "/api/connectors/missing"}, {Method: "GET", Path: "/api/connectors/sample/unknown"}, {Method: "GET", Path: "/api/connectors/sample/check"}, {Method: "DELETE", Path: "/api/connectors/sample"}, {Method: "POST", Path: "/api/connectors"}}
	for _, body := range []string{`{`, `null`, `[]`, `true`, `"text"`, `{"unknown":1}`, `{"acknowledgeSharedCredentials":1}`, `{"sessionId":0}`, `{"sessionId":"   "}`, `{"sessionId":"` + strings.Repeat("a", 257) + `"}`, `{"configuration":{}}`, `{}`, `{"acknowledgeSharedCredentials":true}`} {
		v := body
		requests = append(requests, request{Method: "POST", Path: "/api/connectors/sample/check", Body: &v})
	}
	for _, body := range []string{`{"configuration":null}`, `{"configuration":[]}`, `{"configuration":{"_bad":"x"}}`, `{"configuration":{"apiKey":false}}`, `{"configuration":{"apiKey":"\n"}}`, `{"configuration":{"apiKey":"` + strings.Repeat("x", 8193) + `"}}`} {
		v := body
		requests = append(requests, request{Method: "POST", Path: "/api/connectors/sample/prepare", Body: &v})
	}
	for _, path := range []string{"/api/connectors/sample/cancel", "/api/connectors/missing/check", "/api/connectors/sample/check/extra"} {
		v := `{}`
		requests = append(requests, request{Method: "POST", Path: path, Body: &v})
	}
	definition := connectorDef("cli", "shared")
	io := &controlledIO{calls: [][]any{}, def: definition}
	service, err := New(Options{Root: filepath.Join(home, "go-connectors"), Platform: "darwin-arm64", Definitions: []protocol.JSONObject{definition}, Bridge: controlledBridge{io}, CreateAdapter: func(protocol.JSONObject, string) (Adapter, error) {
		t.Error("guard fixture started a native action")
		return io, nil
	}, Now: time.Now, UUID: func() string { return "guard" }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	actual := []any{}
	for _, r := range requests {
		var body string
		if r.Body != nil {
			body = *r.Body
		}
		req := httptest.NewRequest(r.Method, r.Path, strings.NewReader(body))
		response := httptest.NewRecorder()
		service.HandleHTTP(response, req, strings.Split(strings.Trim(req.URL.Path, "/"), "/"))
		var value any
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, map[string]any{"status": response.Code, "body": value, "contentType": response.Header().Get("Content-Type")})
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-connectors-http.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CONNECTOR_HTTP_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"definition": definition, "requests": requests})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	expected, err := command.Output()
	if err != nil {
		t.Fatalf("Source HTTP: %v %s", err, stderr.String())
	}
	data, _ := json.Marshal(actual)
	var a, b any
	_ = json.Unmarshal(data, &a)
	if err := json.Unmarshal(expected, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		al, bl := a.([]any), b.([]any)
		for i, value := range al {
			if !reflect.DeepEqual(value, bl[i]) {
				t.Errorf("request %d %s: Go=%s Source=%s", i, requests[i].Path, raw(value), raw(bl[i]))
			}
		}
	}
}
