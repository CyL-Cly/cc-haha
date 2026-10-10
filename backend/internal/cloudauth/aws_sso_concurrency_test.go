package cloudauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSSOConcurrentProfilesShareRefreshThrottleAndWriteFailureKeepsUsableResult(t *testing.T) {
	home := t.TempDir()
	now := time.Now().UTC()
	tokenPath := SSOTokenPath(home, "private-session")
	config := filepath.Join(home, "config")
	credentials := filepath.Join(home, "credentials")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0700); err != nil {
		t.Fatal(err)
	}
	token := map[string]any{"accessToken": "OLD_PRIVATE", "expiresAt": now.Add(time.Minute).Format(time.RFC3339Nano), "clientId": "C", "clientSecret": "S", "refreshToken": "R", "unknown": true}
	raw, _ := json.Marshal(token)
	if err := os.WriteFile(tokenPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentials, nil, 0600); err != nil {
		t.Fatal(err)
	}
	data := "[profile first]\nsso_account_id=123\nsso_role_name=Role\nsso_session=private-session\n[profile second]\nsso_account_id=123\nsso_role_name=Role\nsso_session=private-session\n[sso-session private-session]\nsso_start_url=https://private.awsapps.com/start\nsso_region=us-east-1\n"
	if err := os.WriteFile(config, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var refresh, writes atomic.Int32
	var mu sync.Mutex
	seen := []string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			refresh.Add(1)
			close(started)
			<-release
			_, _ = io.WriteString(w, `{"accessToken":"NEW_PRIVATE","expiresIn":3600,"refreshToken":"NEXT_PRIVATE"}`)
			return
		}
		mu.Lock()
		seen = append(seen, r.Header.Get("x-amz-sso_bearer_token"))
		mu.Unlock()
		_, _ = io.WriteString(w, `{"roleCredentials":{"accessKeyId":"A","secretAccessKey":"B","sessionToken":"C","expiration":1900000000000}}`)
	}))
	defer peer.Close()
	state := &SSOState{}
	resolver := func(profile string) *AWSResolver {
		r, err := NewAWSResolver(AWSOptions{Home: home, Environment: map[string]string{"AWS_PROFILE": profile, "AWS_CONFIG_FILE": config, "AWS_SHARED_CREDENTIALS_FILE": credentials}, ReadFile: NativeAWSReadFile, Now: func() time.Time { return now }, WriteFile: func(context.Context, string, []byte) error { writes.Add(1); return os.ErrPermission }, SSO: &SSO{State: state, Do: peer.Client().Do, RoleEndpoint: peer.URL, OIDCEndpoint: peer.URL}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := resolver("first")
	second := resolver("second")
	done := make(chan error, 1)
	go func() { _, err := first.Resolve(context.Background()); done <- err }()
	<-started
	if _, err := second.Resolve(context.Background()); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if refresh.Load() != 1 || writes.Load() != 1 {
		t.Fatal("refresh was duplicated", refresh.Load(), writes.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "OLD_PRIVATE" || seen[1] != "NEW_PRIVATE" {
		t.Fatal("original in-flight refresh throttle behavior", seen)
	}
	persisted, err := os.ReadFile(tokenPath)
	if err != nil || string(persisted) != string(raw) {
		t.Fatal("failed best-effort write altered old file", err)
	}
}
