package server

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
	"testing"
	"time"
)

func TestSessionCollaborationAuthenticationMatchesSourceBeforeOrdinaryCORS(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	type row struct {
		Path    string      `json:"path"`
		Method  string      `json:"method"`
		Headers [][2]string `json:"headers"`
	}
	valid := [][2]string{{"x-session-id", "caller"}, {"authorization", "Bearer session-secret"}}
	rows := []row{{"/api/session-collaboration/list", "POST", valid}, {"/api/session-collaboration/read", "POST", valid}, {"/api/session-collaboration/create", "POST", valid}, {"/api/session-collaboration/send", "POST", valid}, {"/api/session-collaboration/wait", "POST", valid}, {"/api/session-collaboration/id/stop", "POST", valid}, {"/api/settings", "POST", valid}, {"/api/session-collaboration/%6cist", "POST", valid}, {"/api/session-collaboration/list/", "POST", valid}, {"/api/session-collaboration/list", "GET", valid}, {"/api/session-collaboration/list", "POST", append(append([][2]string{}, valid...), [2]string{"origin", ""})}, {"/api/session-collaboration/list", "POST", append(append([][2]string{}, valid...), [2]string{"origin", "https://example.com"})}, {"/api/session-collaboration/list", "POST", [][2]string{{"x-session-id", "caller"}, {"authorization", "Bearer global-key"}}}, {"/api/session-collaboration/list", "POST", append(append([][2]string{}, valid...), [2]string{"authorization", "Bearer other"})}, {"/api/session-collaboration/list", "POST", append(append([][2]string{}, valid...), [2]string{"x-session-id", "other"})}}
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	s, host := testHost(t, options)
	s.collaboration.mu.Lock()
	s.collaboration.tokens["caller"] = "session-secret"
	s.collaboration.mu.Unlock()
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-bridge.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "endpoint": host.URL, "configurations": []any{}, "credentials": rows, "responses": []any{}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SESSION_BRIDGE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source auth %v %.2000s", e, stderr.String())
	}
	var source struct {
		Auth         []struct{ Action, Caller *string }
		Capabilities struct{ Blocked []any }
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source.Auth) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Source auth shape", e, string(data))
	}
	for index, row := range rows {
		r := httptest.NewRequest(row.Method, "http://127.0.0.1"+row.Path, nil)
		for _, header := range row.Headers {
			r.Header.Add(header[0], header[1])
		}
		var action, caller *string
		if value := collaborationAction(r.URL.EscapedPath()); value != "" {
			action = &value
		}
		if value := s.collaborationCaller(r); value != "" {
			caller = &value
		}
		if !optionalStringEqual(action, source.Auth[index].Action) || !optionalStringEqual(caller, source.Auth[index].Caller) {
			t.Errorf("auth%d changed action%v caller%v source%v", index, action, caller, source.Auth[index])
		}
	}
	for _, method := range []string{"GET", "OPTIONS", "POST"} {
		r, _ := http.NewRequest(method, host.URL+"/api/session-collaboration/list", nil)
		r.Header.Set("Origin", "https://untrusted.example")
		r.Header.Set("Authorization", "Bearer session-secret")
		r.Header.Set("X-Session-Id", "caller")
		response, e := host.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 401 || string(bytes.TrimSpace(body)) != `{"error":"Invalid session credential"}` || response.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("credential branch did not precede ordinary CORS", response.StatusCode, string(body))
		}
	}
}
func optionalStringEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func TestSessionCollaborationCredentialRevokedWithOwnedRuntimeAndOnShutdown(t *testing.T) {
	opened := make(chan SessionOptions, 2)
	options := testOptions(t, func(_ context.Context, o SessionOptions, _ Emit) (LiveSession, error) {
		opened <- o
		return &transportSession{}, nil
	})
	s, host := testHost(t, options)
	s.setCollaborationEndpoint(host.Listener.Addr())
	id := createTestSession(t, host, options.WorkDir)
	if _, e := s.live(id); e != nil {
		t.Fatal(e)
	}
	first := <-opened
	if first.Environment["CC_HAHA_DESKTOP_SERVER_URL"] != host.URL || first.OnSDK == nil {
		t.Fatal("actual factory did not receive owned bridge producer", first.Environment)
	}
	r := httptest.NewRequest("POST", host.URL+"/api/session-collaboration/list", nil)
	r.Header.Set("X-Session-Id", id)
	r.Header.Set("Authorization", "Bearer "+first.Environment["CC_HAHA_SESSION_COLLABORATION_TOKEN"])
	if s.collaborationCaller(r) != id {
		t.Fatal("owned credential not registered")
	}
	if e := s.stopSessionAndWait(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	if s.collaborationCaller(r) != "" {
		t.Fatal("retired credential retained")
	}
	if _, e := s.live(id); e != nil {
		t.Fatal(e)
	}
	second := <-opened
	if second.Environment["CC_HAHA_SESSION_COLLABORATION_TOKEN"] == first.Environment["CC_HAHA_SESSION_COLLABORATION_TOKEN"] {
		t.Fatal("reopened runtime reused stale credential")
	}
	if e := s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Authorization", "Bearer "+second.Environment["CC_HAHA_SESSION_COLLABORATION_TOKEN"])
	if s.collaborationCaller(r) != "" {
		t.Fatal("shutdown did not revoke credential")
	}
}
