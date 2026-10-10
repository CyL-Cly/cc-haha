package server

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

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestHahaOAuthManagementAndCallbackMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	clock := int64(1770000000000)
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config}
	service, err := desktopoauth.New(desktopoauth.Options{Home: home, ConfigDir: config, Platform: "linux", Environment: func() map[string]string { return env }, Now: func() time.Time { return time.UnixMilli(clock) }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	type input struct {
		Method string              `json:"method"`
		Path   string              `json:"path"`
		Body   *string             `json:"body,omitempty"`
		Tokens protocol.JSONObject `json:"tokens,omitzero"`
	}
	s := func(v string) *string { return &v }
	tokens := protocol.NewJSONObject(protocol.JSONField{Name: "accessToken", Value: []byte(`"fake-private-access"`)}, protocol.JSONField{Name: "refreshToken", Value: []byte(`"fake-private-refresh"`)}, protocol.JSONField{Name: "expiresAt", Value: []byte(`1770003600000`)}, protocol.JSONField{Name: "scopes", Value: []byte(`["user:inference"]`)}, protocol.JSONField{Name: "subscriptionType", Value: []byte(`"max"`)})
	inputs := []input{{Method: "GET", Path: "/api/haha-oauth"}, {Method: "POST", Path: "/api/haha-oauth/start", Body: s(`{`)}, {Method: "POST", Path: "/api/haha-oauth/start", Body: s(`{}`)}, {Method: "POST", Path: "/api/haha-oauth/start", Body: s(`{"serverPort":"3456"}`)}, {Method: "POST", Path: "/api/haha-oauth/start", Body: s(`{"serverPort":-1}`)}, {Method: "POST", Path: "/api/haha-oauth/start", Body: s(`{"serverPort":0.5}`)}, {Method: "GET", Path: "/api/haha-oauth/status", Tokens: tokens}, {Method: "GET", Path: "/callback?code=x&state=unknown"}, {Method: "GET", Path: "/api/haha-oauth/callback?code=x"}, {Method: "GET", Path: "/callback?error=%3Cscript%3E%26%22%27"}, {Method: "PUT", Path: "/api/haha-oauth"}, {Method: "GET", Path: "/api/haha-oauth/unknown"}, {Method: "DELETE", Path: "/api/haha-oauth"}, {Method: "GET", Path: "/api/haha-oauth/status"}}
	actual := []any{}
	server := &Server{}
	for _, item := range inputs {
		if item.Tokens.IsObject() {
			if err = service.SaveTokens(context.Background(), item.Tokens); err != nil {
				t.Fatal(err)
			}
		}
		body := ""
		if item.Body != nil {
			body = *item.Body
		}
		req := httptest.NewRequest(item.Method, "http://localhost:3456"+item.Path, strings.NewReader(body))
		writer := httptest.NewRecorder()
		if strings.HasPrefix(item.Path, "/callback") {
			err = server.hahaOAuthCallback(writer, req, service)
		} else {
			err = server.hahaOAuthHTTP(writer, req, strings.Split(strings.Trim(req.URL.Path, "/"), "/"), service)
		}
		if err != nil {
			writeError(writer, err)
		}
		html := strings.HasPrefix(writer.Header().Get("Content-Type"), "text/html")
		var value any = writer.Body.String()
		if !html {
			if json.Unmarshal(writer.Body.Bytes(), &value) != nil {
				t.Fatal("Go JSON")
			}
		}
		actual = append(actual, map[string]any{"status": writer.Code, "html": html, "body": value})
		if strings.Contains(writer.Body.String(), "fake-private") {
			t.Fatal("status leaked credential")
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-oauth.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_DESKTOP_OAUTH_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"clock": clock, "api": inputs})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source API %v %.1500s", err, stderr.String())
	}
	actualJSON, _ := json.Marshal(actual)
	var a, b any
	_ = json.Unmarshal(actualJSON, &a)
	if json.Unmarshal(output, &b) != nil {
		t.Fatalf("Source JSON %.1500s", output)
	}
	if !reflect.DeepEqual(a, b) {
		al, bl := a.([]any), b.([]any)
		for i, value := range al {
			if !reflect.DeepEqual(value, bl[i]) {
				got, _ := json.Marshal(value)
				want, _ := json.Marshal(bl[i])
				t.Errorf("case%d Go=%.1700s Source=%.1700s", i, got, want)
			}
		}
	}
}
