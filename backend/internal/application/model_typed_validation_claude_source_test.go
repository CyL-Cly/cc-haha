package application

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestTypedCustomModelActualSourceOrdinaryCLIStoredClaudeIdentityAndNonstreamAuth(t *testing.T) {
	type request struct {
		Body    map[string]any
		Headers http.Header
	}
	var mu sync.Mutex
	seen := map[string][]request{}
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		client := r.Header.Get("X-M680-Client")
		seen[client] = append(seen[client], request{body, r.Header.Clone()})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"owned-claude-validation","content":[],"usage":{}}`))
	}))
	defer peer.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw})
	secure := map[string]any{"claudeAiOauth": map[string]any{"accessToken": "owned-fake-claude-token", "refreshToken": "owned-fake-refresh", "expiresAt": 2000000000000, "scopes": []string{"user:profile", "user:inference"}, "subscriptionType": "max", "rateLimitTier": "default_claude_max_5x"}, "future": true}
	row := typedModelSourceRow{Name: "ordinary-stored-Claude", Args: "MiXeD-CLI-Custom", Settings: map[string]any{"model": "sonnet", "future": true}, State: map[string]any{"mainLoopModel": "sonnet", "mainLoopModelForSession": nil}, Env: map[string]string{"CLAUDE_CODE_ENTRYPOINT": "cli", "ANTHROPIC_API_KEY": ""}}
	want := runTypedModelSourceCredentials(t, []typedModelSourceRow{row}, peer.URL, secure, string(cert))
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Arguments.Model = "sonnet"
	certPath := filepath.Join(t.TempDir(), "root.pem")
	_ = os.WriteFile(certPath, cert, 0600)
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CLAUDE_CODE_ENTRYPOINT"] = "cli"
		env["NODE_ENV"] = "production"
		env["ANTHROPIC_API_KEY"] = ""
		env["NODE_EXTRA_CA_CERTS"] = certPath
		env["ANTHROPIC_CUSTOM_HEADERS"] = "X-M680-Client: Go"
	}
	secureRaw, _ := json.Marshal(secure)
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return secureRaw, nil }
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"model":"sonnet","future":true}`), 0600)
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	var notice string
	if e = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model " + row.Args}, func(event tui.Event) error {
		if event.Kind == tui.Notice {
			notice = event.Text
		}
		return nil
	}); e != nil {
		t.Fatal("actual Source stored-Claude validation", want[0].Notice, e)
	}
	if notice != want[0].Notice {
		t.Fatal("Source notice", notice, want[0].Notice)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen["Source"]) != 1 || len(seen["Go"]) != 1 {
		t.Fatal("actual Source auth requests", seen)
	}
	source, got := seen["Source"][0], seen["Go"][0]
	for _, key := range []string{"Authorization", "X-Api-Key", "Anthropic-Beta"} {
		if !reflect.DeepEqual(got.Headers.Values(key), source.Headers.Values(key)) {
			t.Fatalf("original CLI identity/header %s Go=%#v Source=%#v", key, got.Headers.Values(key), source.Headers.Values(key))
		}
	}
	for _, key := range []string{"model", "max_tokens", "messages", "tools", "stream", "thinking"} {
		if !reflect.DeepEqual(got.Body[key], source.Body[key]) {
			t.Fatal("original stored auth policy", key, got.Body[key], source.Body[key])
		}
	}
}
