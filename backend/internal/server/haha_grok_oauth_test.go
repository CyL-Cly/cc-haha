package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
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

func TestDesktopGrokAPIAndDedicatedCallbackMatchActualSource(t *testing.T) {
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
	rows := []map[string]any{{"method": "GET", "path": "/api/haha-grok-oauth"}}
	for _, body := range []string{"{", `{}`, `null`, `{"serverPort":-1}`} {
		rows = append(rows, map[string]any{"method": "POST", "path": "/api/haha-grok-oauth/start", "body": body})
	}
	rows = append(rows, map[string]any{"method": "GET", "path": "/api/haha-grok-oauth/success"}, map[string]any{"method": "GET", "path": "/api/haha-grok-oauth", "tokens": map[string]any{"accessToken": "fake-grok", "refreshToken": "fake-refresh", "expiresAt": now + 3600000, "email": "fixture@invalid", "unknown": true}}, map[string]any{"method": "GET", "path": "/api/haha-grok-oauth/status"}, map[string]any{"method": "PUT", "path": "/api/haha-grok-oauth"}, map[string]any{"method": "DELETE", "path": "/api/haha-grok-oauth"}, map[string]any{"method": "GET", "path": "/api/haha-grok-oauth"}, map[string]any{"method": "GET", "path": "/api/haha-grok-oauth", "tokens": "{broken"})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-grok-api.fixture.ts")
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
	_ = parent.Grok().DeleteTokens(context.Background())
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("account API created a model session")
		return nil, nil
	})
	o.HahaGrokOAuth = parent.Grok()
	o.Authorization = access.Authorization{LocalToken: "fixture-local"}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	for i, row := range rows {
		if tokens, ok := row["tokens"]; ok {
			_ = os.MkdirAll(filepath.Dir(parent.Grok().TokenPath()), 0700)
			if body, ok := tokens.(string); ok {
				_ = os.WriteFile(parent.Grok().TokenPath(), []byte(body), 0600)
			} else {
				data, _ := json.Marshal(tokens)
				_ = os.WriteFile(parent.Grok().TokenPath(), data, 0600)
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
		isHTML := strings.Contains(w.Header().Get("Content-Type"), "text/html")
		if isHTML {
			value = w.Body.String()
		} else {
			if json.Unmarshal(w.Body.Bytes(), &value) != nil {
				t.Fatal(w.Body.String())
			}
		}
		if strings.HasSuffix(row["path"].(string), "/start") && w.Code == 200 {
			body := value.(map[string]any)
			auth, err := url.Parse(body["authorizeUrl"].(string))
			if err != nil {
				t.Fatal(err)
			}
			query := auth.Query()
			redirect, err := url.Parse(query.Get("redirect_uri"))
			if err != nil {
				t.Fatal(err)
			}
			keys := []string{}
			for _, pair := range strings.Split(auth.RawQuery, "&") {
				key, _, _ := strings.Cut(pair, "=")
				key, _ = url.QueryUnescape(key)
				keys = append(keys, key)
			}
			value = map[string]any{"stateLength": float64(len(body["state"].(string))), "issuer": auth.Scheme + "://" + auth.Host, "path": auth.Path, "keys": stringsToAnyGrok(keys), "clientId": query.Get("client_id"), "redirectHost": redirect.Hostname(), "redirectPath": redirect.Path, "method": query.Get("code_challenge_method"), "challengeLength": float64(len(query.Get("code_challenge"))), "nonceLength": float64(len(query.Get("nonce")))}
		}
		var want any
		_ = json.Unmarshal(source[i], &want)
		got := map[string]any{"status": float64(w.Code), "body": value}
		if isHTML {
			got["cache"] = w.Header().Get("Cache-Control")
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d got%v want%v", i, got, want)
		}
	}
}

func stringsToAnyGrok(values []string) []any {
	result := []any{}
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
