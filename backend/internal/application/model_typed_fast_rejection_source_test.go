package application

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
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

func TestTypedModelFastActualSourceRejectionNotificationStateAndPersistence(t *testing.T) {
	for _, reason := range []string{"org_disabled", "member_level_disabled", "out_of_credits"} {
		t.Run(reason, func(t *testing.T) {
			var mu sync.Mutex
			seen := map[string][]map[string]any{}
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				var body map[string]any
				_ = json.Unmarshal(b, &body)
				client := r.Header.Get("X-M680-Client")
				mu.Lock()
				seen[client] = append(seen[client], body)
				first := len(seen[client]) == 1
				mu.Unlock()
				if first {
					w.Header().Set("Content-Type", "application/json")
					if reason == "org_disabled" {
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"Fast mode is not enabled"}}`))
					} else {
						w.Header().Set("Anthropic-Ratelimit-Unified-Overage-Disabled-Reason", reason)
						w.WriteHeader(429)
						_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"owned rejected fast"}}`))
					}
					return
				}
				response(w, "fast-reject", "", nil, "actual rejection retry")
			}))
			defer peer.Close()
			cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw})
			rows := []typedModelSourceRow{}
			for i := 0; i < 2; i++ {
				rows = append(rows, typedModelSourceRow{Name: "opus", Args: "opus", Settings: map[string]any{"model": "opus", "fastMode": true, "future": true}, State: map[string]any{"mainLoopModel": "opus", "mainLoopModelForSession": nil}, Env: map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-6", "CLAUDE_CODE_MAX_RETRIES": "1"}, Global: map[string]any{"penguinModeOrgEnabled": true}, InitializeFast: true, Ask: true, ContinueStore: i > 0, Notifications: true})
			}
			want := runTypedModelSource(t, rows, peer.URL, string(cert))
			o := nativeSessionOptions(t, peer.URL)
			o.Interactive = true
			o.Arguments.Model = "opus"
			certPath := filepath.Join(t.TempDir(), "root.pem")
			_ = os.WriteFile(certPath, cert, 0600)
			for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
				delete(env, "CLAUDE_CODE_DISABLE_FAST_MODE")
				env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "claude-opus-4-6"
				env["ANTHROPIC_CUSTOM_HEADERS"] = "X-M680-Client: Go"
				env["NODE_EXTRA_CA_CERTS"] = certPath
				env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] = "1"
				env["CLAUDE_CODE_MAX_RETRIES"] = "1"
			}
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			_ = os.MkdirAll(config, 0700)
			_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"model":"opus","fastMode":true,"future":true}`), 0600)
			_ = os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"penguinModeOrgEnabled":true,"future":true}`), 0600)
			s, e := NewSession(context.Background(), o)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			var notices []tui.Event
			unsubscribe := s.SubscribeEnvironmentHookNotices(func(event tui.Event) { notices = append(notices, event) })
			defer unsubscribe()
			for i, row := range rows {
				var notice string
				if e = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model " + row.Args}, func(event tui.Event) error {
					if event.Kind == tui.Notice {
						notice = event.Text
					}
					return nil
				}); e != nil {
					t.Fatal(e)
				}
				if notice != want[i].Notice {
					t.Fatal("Source notice", i, notice, want[i].Notice)
				}
				if e = s.Submit(context.Background(), tui.Input{Text: "actual fast model stream"}, nil); e != nil {
					t.Fatal(e)
				}
				if e = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					state, e := s.process.Controller.TypedModelStateOnOwner(scope)
					if e == nil && state.FastMode != want[i].State["fastMode"] {
						t.Errorf("original notification AppState %v Go %v", want[i].State["fastMode"], state.FastMode)
					}
					return e
				}); e != nil {
					t.Fatal(e)
				}
				b, e := os.ReadFile(filepath.Join(config, "settings.json"))
				var settings map[string]any
				_ = json.Unmarshal(b, &settings)
				if e != nil || !reflect.DeepEqual(settings, want[i].Settings) {
					t.Fatal("original persisted fields", settings, want[i].Settings, e)
				}
			}
			notification := want[0].State["notifications"].(map[string]any)["current"].(map[string]any)
			if len(notices) != 1 || notices[0].Text != notification["text"] {
				t.Fatal("Source real useFastModeNotification", notices, notification)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(seen["Source"]) != 3 || len(seen["Go"]) != 3 {
				t.Fatal(seen)
			}
			for i, w := range seen["Source"] {
				if !reflect.DeepEqual(seen["Go"][i]["speed"], w["speed"]) {
					t.Fatal("Source next speed", i, seen["Go"][i]["speed"], w["speed"])
				}
			}
		})
	}
}
