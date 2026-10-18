package ccrsessions

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActualSourceRemoteAgentRestoreUsesSidecarsStatusAndPhysicalPoll(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{
		filepath.Join(config, ".credentials.json"): `{"claudeAiOauth":{"accessToken":"owned-remote-task-token","refreshToken":"owned-future-refresh-unused","expiresAt":4102444800000,"scopes":["user:inference","user:profile","user:sessions:claude_code"],"subscriptionType":"max"}}`,
		filepath.Join(config, ".claude.json"):      `{"oauthAccount":{"accountUuid":"owned-account","organizationUuid":"owned-org"},"hasCompletedOnboarding":true}`,
	} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var requests []string
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owned-remote-task-token" || r.Header.Get("x-organization-uuid") != "owned-org" || r.Header.Get("anthropic-beta") != "ccr-byoc-2025-07-29" {
			t.Error("actual Source task auth headers differ", r.Header)
		}
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		switch r.URL.Path {
		case "/v1/sessions/session-owned-running":
			ccrJSON(w, map[string]any{"session_status": "running", "session_context": map[string]any{}})
		case "/v1/sessions/session-owned-running/events":
			ccrJSON(w, map[string]any{"data": []any{
				map[string]any{"type": "env_manager_log", "session_id": "session-owned-running"},
				map[string]any{"type": "assistant", "session_id": "session-owned-running", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ACTUAL_RESTORED_REMOTE_OUTPUT"}}}},
				map[string]any{"type": "result", "subtype": "success", "session_id": "session-owned-running", "result": "ACTUAL_RESTORED_REMOTE_RESULT"},
			}, "has_more": false, "last_id": "owned-event-last"})
		case "/v1/sessions/session-owned-archived":
			ccrJSON(w, map[string]any{"session_status": "archived"})
		case "/v1/sessions/session-owned-missing":
			w.WriteHeader(http.StatusNotFound)
			ccrJSON(w, map[string]any{"error": map[string]string{"message": "owned missing"}})
		case "/v1/sessions/session-owned-auth":
			w.WriteHeader(http.StatusUnauthorized)
			ccrJSON(w, map[string]any{"error": map[string]string{"message": "owned recoverable auth"}})
		default:
			t.Error("unowned Source task request", r.Method, r.URL)
			w.WriteHeader(http.StatusInternalServerError)
			ccrJSON(w, map[string]any{})
		}
	}))
	defer peer.Close()
	ca := filepath.Join(home, "private-peer-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	metadata := []map[string]any{}
	for _, label := range []string{"running", "archived", "missing", "auth"} {
		metadata = append(metadata, map[string]any{"taskId": "owned-" + label, "remoteTaskType": "remote-agent", "sessionId": "session-owned-" + label, "title": "Owned " + label, "command": "Original remote command " + label, "spawnedAt": 1700000000000, "toolUseId": "owned-tool-" + label})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-remote-agent-restore.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "CLAUDE_CODE_TMPDIR=" + home, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "NODE_EXTRA_CA_CERTS=" + ca, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_REMOTE_AGENT_RESTORE=1"}
	input, _ := json.Marshal(map[string]any{"home": home, "peer": peer.URL, "sid": "12121212-1212-4212-8212-121212121212", "metadata": metadata, "waitResult": "owned-running"})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("actual whole RemoteAgentTask restore", err, string(out), stderr.String())
	}
	var result struct {
		Restored map[string]json.RawMessage
		After    map[string]json.RawMessage
		Files    []struct{ Name, Content string }
		Outputs  []struct {
			TaskID, Path, Content string
			Exists                bool
		}
		Queue []json.RawMessage
		SDK   []json.RawMessage
		HTTP  []json.RawMessage
		Caps  struct{ Blocked []string }
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err, string(out))
	}
	if len(result.Caps.Blocked) != 0 || len(result.Restored) != 1 || len(result.After) != 1 || objectJSON(string(result.After["owned-running"])).String("status") != "completed" || string(objectJSON(string(result.After["owned-running"])).Get("notified")) != "true" || len(result.Queue) != 1 || len(result.SDK) != 0 {
		t.Fatal("Source task/state/notification consumer differs", string(out))
	}
	if !strings.Contains(string(result.Queue[0]), "Remote task") || !strings.Contains(string(result.Queue[0]), "completed successfully") {
		t.Fatal("original queue notification missing", string(out))
	}
	if len(result.Files) != 1 || result.Files[0].Name != "remote-agent-owned-auth.meta.json" {
		t.Fatal("404/archived/completed deletion or recoverable sidecar retention differs", string(out))
	}
	found := false
	for _, output := range result.Outputs {
		if output.TaskID == "owned-running" {
			found = output.Exists && strings.Contains(output.Content, "ACTUAL_RESTORED_REMOTE_OUTPUT")
		}
	}
	if !found {
		t.Fatal("actual poll output was not physically written", string(out))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 6 {
		t.Fatal("Source fetch/event/metadata count differs", requests, string(out))
	}
	t.Log("actual full Source remote task restore/poll/sidecars/notification receipt", string(out))
}

func TestTaskAPIPhysicalPagingMetadataFailureAndClose(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	held, canceled := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		if strings.Contains(r.URL.Path, "held") {
			close(held)
			<-r.Context().Done()
			close(canceled)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/events") {
			if r.URL.Query().Get("after_id") == "" {
				_, _ = io.WriteString(w, `{"data":[{"type":"env_manager_log","session_id":"owned"},{"type":"control_response","session_id":"owned"},{"type":"user","message":{}},{"type":"assistant","session_id":"owned","message":{}}],"has_more":true,"last_id":"a/b 原"}`)
			} else {
				_, _ = io.WriteString(w, `{"data":[{"type":"result","session_id":"owned","subtype":"success"}],"has_more":false,"last_id":"final"}`)
			}
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"owned"}}`)
	}))
	defer peer.Close()
	api, err := NewTaskAPI(TaskAPIOptions{BaseURL: peer.URL, HTTP: peer.Client(), Credentials: func(context.Context, bool) (Credentials, error) {
		return Credentials{Token: "fake-task-token", Organization: "fake-task-org"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	result, err := api.Poll(context.Background(), "owned", nil)
	if err != nil || len(result.Events) != 2 || string(result.LastEventID) != `"final"` || result.SessionStatus != nil {
		t.Fatal("actual paged events or swallowed metadata failure differs", result, err)
	}
	mu.Lock()
	if len(requests) != 3 || requests[1] != "GET /v1/sessions/owned/events?after_id=a%2Fb+%E5%8E%9F" {
		t.Error("actual cursor or separate metadata request differs", requests)
	}
	mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := api.Poll(context.Background(), "held", nil); done <- err }()
	select {
	case <-held:
	case <-time.After(3 * time.Second):
		t.Fatal("actual task HTTP did not start")
	}
	api.Close()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("close did not cancel/join actual held request")
	}
	if err := <-done; err == nil {
		t.Fatal("closed API synthesized successful poll")
	}
}
