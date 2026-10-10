package application

import (
	"context"
	"encoding/json"
	"encoding/pem"
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

func TestTypedModelFastActualSourceQueryStreamKeepsLatchedHeaderAfterDowngrade(t *testing.T) {
	runTypedModelFastActualSourceStream(t, false)
}
func TestTypedModelFastActualSource429CooldownRetryAndTypedSwitchClearsRuntime(t *testing.T) {
	runTypedModelFastActualSourceStream(t, true)
}
func runTypedModelFastActualSourceStream(t *testing.T, cooldown bool) {
	type request struct {
		Body map[string]any
		Beta string
	}
	var mu sync.Mutex
	seen := map[string][]request{}
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		client := r.Header.Get("X-M680-Client")
		seen[client] = append(seen[client], request{body, r.Header.Get("Anthropic-Beta")})
		number := len(seen[client])
		mu.Unlock()
		if cooldown && number == 1 {
			w.Header().Set("Retry-After", "20")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"private fast cooldown"}}`))
			return
		}
		response(w, "actual-fast-stream", "", nil, "ACTUAL_FAST_STREAM")
	}))
	defer peer.Close()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw})
	rows := []typedModelSourceRow{}
	args := []string{"opus", "sonnet", "opus"}
	if cooldown {
		args = []string{"opus", "opus"}
	}
	for i, arg := range args {
		rows = append(rows, typedModelSourceRow{Name: arg, Args: arg, Settings: map[string]any{"model": "opus", "fastMode": true, "future": true}, State: map[string]any{"mainLoopModel": "opus", "mainLoopModelForSession": nil}, Env: map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-6", "CLAUDE_CODE_MAX_RETRIES": "1"}, Global: map[string]any{"penguinModeOrgEnabled": true}, InitializeFast: true, Ask: true, ContinueStore: i > 0})
	}
	want := runTypedModelSource(t, rows, peer.URL, string(certificate))
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.Model = "opus"
	o.Interactive = true
	certPath := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(certPath, certificate, 0600); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		delete(env, "CLAUDE_CODE_DISABLE_FAST_MODE")
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "claude-opus-4-6"
		env["NODE_EXTRA_CA_CERTS"] = certPath
		env["ANTHROPIC_CUSTOM_HEADERS"] = "X-M680-Client: Go"
		env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] = "1"
		env["CLAUDE_CODE_MAX_RETRIES"] = "1"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"model":"opus","fastMode":true,"future":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"penguinModeOrgEnabled":true,"future":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for i, row := range rows {
		var notice string
		if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model " + row.Args}, func(e tui.Event) error {
			if e.Kind == tui.Notice {
				notice = e.Text
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if notice != want[i].Notice {
			t.Fatal("actual Source transition", i, notice, want[i].Notice)
		}
		if err = s.Submit(context.Background(), tui.Input{Text: "actual fast model stream"}, nil); err != nil {
			t.Fatal("actual fast ASK/metadata/accounting chain", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen["Source"]) != 3 || len(seen["Go"]) != 3 {
		t.Fatal("actual request consumers", seen)
	}
	for i, source := range seen["Source"] {
		got := seen["Go"][i]
		for _, key := range []string{"model", "speed", "stream", "max_tokens"} {
			if !reflect.DeepEqual(got.Body[key], source.Body[key]) {
				t.Fatal("actual Source stream policy", i, key, got.Body[key], source.Body[key])
			}
		}
		if got.Beta != source.Beta {
			t.Fatal("actual Source sticky fast beta", i, got.Beta, source.Beta)
		}
	}
}
