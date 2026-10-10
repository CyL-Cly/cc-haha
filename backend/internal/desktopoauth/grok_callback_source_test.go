package desktopoauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestGrokPKCECallbackTokenFileAndPageMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	now := int64(1770000000000)
	reply := map[string]any{"access_token": "private-access", "refresh_token": "private-refresh", "expires_in": 3600}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-grok-callback.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GROK_CALLBACK_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(encode(map[string]any{"now": now, "response": reply}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source callback %v %s", err, stderr.String())
	}
	var want any
	if err = json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err, string(raw))
	}
	var request any
	var verifier string
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		verifier = r.Form.Get("code_verifier")
		redirect, _ := url.Parse(r.Form.Get("redirect_uri"))
		request = map[string]any{"url": "https://auth.x.ai" + r.URL.Path, "method": r.Method, "headers": map[string]string{"accept": r.Header.Get("Accept"), "content-type": r.Header.Get("Content-Type"), "user-agent": r.Header.Get("User-Agent")}, "grant": r.Form.Get("grant_type"), "client": r.Form.Get("client_id"), "code": r.Form.Get("code"), "redirectPath": redirect.Path, "verifierLength": len(verifier)}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encode(reply))
	}))
	defer host.Close()
	private := t.TempDir()
	parent, err := New(Options{Home: private, ConfigDir: filepath.Join(private, ".claude"), Platform: "linux", Environment: func() map[string]string { return map[string]string{} }, Now: func() time.Time { return time.UnixMilli(now) }, HTTPClient: &http.Client{Transport: openAITestTransport{host.URL, http.DefaultTransport}}})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	service := parent.Grok()
	session, err := service.StartSession()
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(session.RedirectURI + "?code=fake-code&state=" + session.State)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(service.TokenPath())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(service.TokenPath())
	if err != nil {
		t.Fatal(err)
	}
	auth, _ := url.Parse(session.AuthorizeURL)
	challenge := sha256.Sum256([]byte(verifier))
	got := map[string]any{"status": response.StatusCode, "contentType": response.Header.Get("Content-Type"), "body": string(body), "request": request, "stored": string(stored), "mode": info.Mode().Perm(), "path": service.TokenPath()[len(private):], "pkceMatches": auth.Query().Get("code_challenge") == base64.RawURLEncoding.EncodeToString(challenge[:]), "consumed": service.GetSession(session.State) == nil}
	var actual any
	_ = json.Unmarshal(encode(got), &actual)
	if !reflect.DeepEqual(actual, want) {
		t.Errorf("actual callback%s want%s", encode(got), raw)
	}
}
