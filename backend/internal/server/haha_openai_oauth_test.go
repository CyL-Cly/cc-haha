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

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
)

func TestDesktopOpenAIAPIAndDedicatedCallbackMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	now := int64(1770000000000)
	parent, e := desktopoauth.New(desktopoauth.Options{Home: home, ConfigDir: config, Platform: "linux", Environment: func() map[string]string { return map[string]string{} }, Now: func() time.Time { return time.UnixMilli(now) }})
	if e != nil {
		t.Fatal(e)
	}
	defer parent.Close()
	rows := []map[string]any{{"method": "GET", "path": "/api/haha-openai-oauth"}}
	for _, body := range []string{"{", "\ufeff{\"serverPort\":0}", `{}`, `null`, `[]`, `{"serverPort":"3456"}`, `{"serverPort":0}`, `{"serverPort":-1}`, `{"serverPort":1.5}`} {
		rows = append(rows, map[string]any{"method": "POST", "path": "/api/haha-openai-oauth/start", "body": body})
	}
	rows = append(rows, map[string]any{"method": "GET", "path": "/api/haha-openai-oauth", "tokens": map[string]any{"accessToken": "fixture-private", "refreshToken": "fake-refresh", "expiresAt": now + 3600000, "email": "fixture@invalid", "accountId": "fixture-account", "unknown": true}}, map[string]any{"method": "GET", "path": "/api/haha-openai-oauth/status"}, map[string]any{"method": "PUT", "path": "/api/haha-openai-oauth"}, map[string]any{"method": "GET", "path": "/api/haha-openai-oauth/unknown"})
	for _, path := range []string{"/auth/callback", "/callback/openai?code=x", "/auth/callback?code=x&state=unknown", "/callback/openai?error=%3Cscript%3E%26%22%27"} {
		rows = append(rows, map[string]any{"method": "GET", "path": path, "callback": true})
	}
	rows = append(rows, map[string]any{"method": "DELETE", "path": "/api/haha-openai-oauth"}, map[string]any{"method": "GET", "path": "/api/haha-openai-oauth"}, map[string]any{"method": "GET", "path": "/api/haha-openai-oauth", "tokens": "{broken"})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-api.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_API_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"now": now, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source API %v %s", e, stderr.String())
	}
	var source []json.RawMessage
	if json.Unmarshal(output, &source) != nil || len(source) != len(rows) {
		t.Fatalf("Source API output%s", output)
	}
	_ = parent.OpenAI().DeleteTokens(context.Background())
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("account API created a model session")
		return nil, nil
	})
	o.HahaOpenAIOAuth = parent.OpenAI()
	o.Authorization = access.Authorization{LocalToken: "fixture-local"}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	for i, row := range rows {
		if tokens, ok := row["tokens"]; ok {
			_ = os.MkdirAll(filepath.Dir(parent.OpenAI().TokenPath()), 0700)
			if body, ok := tokens.(string); ok {
				_ = os.WriteFile(parent.OpenAI().TokenPath(), []byte(body), 0600)
			} else {
				data, _ := json.Marshal(tokens)
				_ = os.WriteFile(parent.OpenAI().TokenPath(), data, 0600)
			}
		}
		body, _ := row["body"].(string)
		r := httptest.NewRequest(row["method"].(string), "http://localhost:3456"+row["path"].(string), strings.NewReader(body))
		if row["callback"] != true {
			r.Header.Set("Authorization", "Bearer fixture-local")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var value any
		if row["callback"] == true {
			value = w.Body.String()
		} else {
			if json.Unmarshal(w.Body.Bytes(), &value) != nil {
				t.Fatal(w.Body.String())
			}
		}
		var want any
		_ = json.Unmarshal(source[i], &want)
		got := map[string]any{"status": float64(w.Code), "body": value}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d got%v want%v", i, got, want)
		}
	}
}
