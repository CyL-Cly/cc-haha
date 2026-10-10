package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSharedMainSSOOIDCRefreshRoleCredentialsWriteReadAndPrivateTokenFile(t *testing.T) {
	var refresh, roles, models atomic.Int32
	var target, tokenPath string
	failures := make(chan error, 10)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			refresh.Add(1)
			var body protocol.JSONObject
			raw, _ := io.ReadAll(r.Body)
			_ = body.UnmarshalJSON(raw)
			if r.Method != "POST" || body.String("grantType") != "refresh_token" || body.String("refreshToken") != "OLD_REFRESH_PRIVATE" || body.String("clientId") != "CLIENT_PRIVATE" || body.String("clientSecret") != "SECRET_PRIVATE" || r.Header.Get("Authorization") != "" {
				failures <- fmt.Errorf("OIDC request did not preserve original unsigned refresh contract")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"accessToken":"REFRESHED_PRIVATE","expiresIn":3600,"refreshToken":"NEXT_REFRESH_PRIVATE"}`)
			return
		case "/federation/credentials":
			roles.Add(1)
			if r.Method != "GET" || r.URL.Query().Get("role_name") != "Private Role" || r.URL.Query().Get("account_id") != "123456789012" || r.Header.Get("x-amz-sso_bearer_token") != "REFRESHED_PRIVATE" || r.Header.Get("Authorization") != "" {
				failures <- fmt.Errorf("role credentials were not obtained through original SSO bearer request")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"roleCredentials":{"accessKeyId":"SSO_PRIVATE","secretAccessKey":"SSO_SECRET","sessionToken":"SSO_SESSION","expiration":1900000000000}}`)
			return
		}
		round := int(models.Add(1))
		raw, _ := io.ReadAll(r.Body)
		var body protocol.JSONObject
		if err := body.UnmarshalJSON(raw); err != nil {
			failures <- err
			return
		}
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=SSO_PRIVATE/") || r.Header.Get("X-Amz-Security-Token") != "SSO_SESSION" || body.Get("model") != nil || body.Get("stream") != nil {
			failures <- fmt.Errorf("physical model request did not use actual SSO credentials")
			return
		}
		tool := ""
		input := map[string]any{}
		if round == 1 {
			tool = "Write"
			input = map[string]any{"file_path": target, "content": "SSO_ACTUAL_SHARED_HARNESS"}
		} else if round == 2 {
			tool = "Read"
			input = map[string]any{"file_path": target}
		}
		recorder := httptest.NewRecorder()
		emitPlanFixture(recorder, round, tool, "SSO_WRITE_READ_DONE")
		wire := recorder.Body.String()
		if tool != "" {
			encoded, _ := json.Marshal(input)
			quoted, _ := json.Marshal(string(encoded))
			wire = strings.Replace(wire, `"partial_json":"{}"`, `"partial_json":`+string(quoted), 1)
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		for _, frame := range strings.Split(wire, "\n\n") {
			for _, line := range strings.Split(frame, "\n") {
				if strings.HasPrefix(line, "data: ") {
					_, _ = w.Write(cloudFixtureEvent(strings.TrimPrefix(line, "data: ")))
				}
			}
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		target = filepath.Join(n.CWD, "sso-write-read.txt")
		tokenPath = cloudauth.SSOTokenPath(n.Home, "private-session")
		config := filepath.Join(n.Home, ".aws", "config")
		if err := os.MkdirAll(filepath.Dir(tokenPath), 0700); err != nil {
			t.Fatal(err)
		}
		raw := fmt.Sprintf(`{"accessToken":"OLD_ACCESS_PRIVATE","expiresAt":%q,"clientId":"CLIENT_PRIVATE","clientSecret":"SECRET_PRIVATE","refreshToken":"OLD_REFRESH_PRIVATE","unknown":{"nested":[true,"\\ud800"]}}`, n.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano))
		if err := os.WriteFile(tokenPath, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config, []byte("[profile selected]\nsso_account_id=123456789012\nsso_role_name=Private Role\nsso_session=private-session\n[sso-session private-session]\nsso_start_url=https://private.awsapps.com/start\nsso_region=us-west-2\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for key, value := range map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "ANTHROPIC_BEDROCK_BASE_URL": peer.URL, "AWS_REGION": "us-east-1", "AWS_PROFILE": "selected", "AWS_CONFIG_FILE": config, "AWS_ENDPOINT_URL_SSO": peer.URL, "AWS_ENDPOINT_URL_SSO_OIDC": peer.URL, "AWS_EC2_METADATA_DISABLED": "true"} {
			n.Environment[key] = value
		}
		delete(n.Environment, "AWS_ACCESS_KEY_ID")
		delete(n.Environment, "AWS_SECRET_ACCESS_KEY")
	}, func(o *MainPrintOptions) {
		o.Arguments.Model = "us.anthropic.claude-sonnet-4-20250514-v1:0"
		o.Arguments.Tools = []string{"Write,Read"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Write the marker and read it with the actual native file tools."})})
	if err := m.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	if refresh.Load() != 1 || roles.Load() != 3 || models.Load() != 3 {
		t.Fatal("unexpected actual network counts", refresh.Load(), roles.Load(), models.Load())
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "SSO_ACTUAL_SHARED_HARNESS" {
		t.Fatal("native tool result", string(raw), err)
	}
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	var token protocol.JSONObject
	if token.UnmarshalJSON(tokenBytes) != nil || token.String("accessToken") != "REFRESHED_PRIVATE" || token.String("refreshToken") != "NEXT_REFRESH_PRIVATE" || !bytes.Contains(token.Get("unknown"), []byte(`nested`)) {
		t.Fatal("original token file contract not preserved")
	}
	info, err := os.Stat(tokenPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("existing token mode changed", err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(nPrivateConfig(m), "projects", "*", m.Controller.SessionID()+".jsonl"))
	if len(matches) != 1 {
		t.Fatal("missing canonical transcript", matches)
	}
	transcript, err := os.ReadFile(matches[0])
	if err != nil || !bytes.Contains(transcript, []byte("SSO_ACTUAL_SHARED_HARNESS")) {
		t.Fatal("canonical native tool history", err)
	}
}
func nPrivateConfig(m *MainPrint) string { return m.options.Native.Environment["CLAUDE_CONFIG_DIR"] }

func TestActualMainSSORefreshCancellationClosesPhysicalPeerAndKeepsToken(t *testing.T) {
	started := make(chan struct{})
	closed := make(chan struct{})
	var path string
	var before []byte
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Error("unexpected model/role request before credentials settled")
			w.WriteHeader(500)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(closed)
	}))
	defer peer.Close()
	defer peer.CloseClientConnections()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		path = cloudauth.SSOTokenPath(n.Home, "cancel-session")
		config := filepath.Join(n.Home, ".aws", "config")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		before = []byte(fmt.Sprintf(`{"accessToken":"OLD_PRIVATE","expiresAt":%q,"clientId":"CLIENT_PRIVATE","clientSecret":"SECRET_PRIVATE","refreshToken":"OLD_REFRESH_PRIVATE","unknown":true}`, n.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)))
		if err := os.WriteFile(path, before, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config, []byte("[profile selected]\nsso_account_id=123\nsso_role_name=Role\nsso_session=cancel-session\n[sso-session cancel-session]\nsso_start_url=https://private.awsapps.com/start\nsso_region=us-east-1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for key, value := range map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "ANTHROPIC_BEDROCK_BASE_URL": peer.URL, "AWS_REGION": "us-east-1", "AWS_PROFILE": "selected", "AWS_CONFIG_FILE": config, "AWS_ENDPOINT_URL_SSO": peer.URL, "AWS_ENDPOINT_URL_SSO_OIDC": peer.URL, "AWS_EC2_METADATA_DISABLED": "true"} {
			n.Environment[key] = value
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Model = "us.anthropic.claude-sonnet-4-20250514-v1:0"
		o.Arguments.Tools = []string{"Read"}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Read the marker."})}))
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("actual OIDC producer did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel lost")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not join ASK")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("native HTTP socket escaped cancellation")
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, before) {
		t.Fatal("canceled refresh wrote token", err)
	}
	cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err = m.ShutdownSession(cleanup); err != nil {
		t.Fatal(err)
	}
}
