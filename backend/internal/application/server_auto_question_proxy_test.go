package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/autoquestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerproxytransport"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Exercise the production target resolver and actual upstream IO. The Source
// fixture imports the original decision service and real proxy handler.
func TestDesktopAutomaticQuestionProxyMatchesOriginalPhysicalRequests(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	for _, format := range []string{"anthropic", "openai_chat", "openai_responses"} {
		for _, status := range []int{200, 400, 422, 403} {
			t.Run(fmt.Sprintf("%s/%d", format, status), func(t *testing.T) {
				type wire struct {
					Path    string
					Body    any
					Headers map[string]string
				}
				var mu sync.Mutex
				requests := []wire{}
				answer := `{"answers":[{"questionIndex":0,"optionLabels":["JSON"]}]}`
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body any
					_ = json.NewDecoder(r.Body).Decode(&body)
					headers := map[string]string{}
					for _, name := range []string{"Content-Type", "Anthropic-Version", "Anthropic-Beta", "Authorization", "X-Api-Key", "X-Claude-Code-Session-Id"} {
						headers[name] = r.Header.Get(name)
					}
					mu.Lock()
					requests = append(requests, wire{r.URL.Path, body, headers})
					first := len(requests) == 1
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					if first && status != 200 {
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"error":{"message":"private fixture rejection"}}`))
						return
					}
					switch format {
					case "openai_chat":
						_, _ = w.Write(raw(map[string]any{"id": "private", "choices": []any{map[string]any{"index": 0, "message": map[string]string{"content": answer}, "finish_reason": "stop"}}}))
					case "openai_responses":
						_, _ = w.Write(raw(map[string]any{"id": "private", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": answer}}}}}))
					default:
						_, _ = w.Write(raw(map[string]any{"content": []any{map[string]string{"type": "text", "text": answer}}}))
					}
				}))
				defer upstream.Close()
				provider := map[string]any{"presetId": "custom", "name": "Private original provider", "baseUrl": upstream.URL + "/root/", "apiKey": "fake-question-key", "authStrategy": "auth_token", "apiFormat": format, "supportsNestedToolResultMedia": false, "models": map[string]string{"main": "large-question-model", "haiku": "small-question-model[1m]", "sonnet": "", "opus": ""}}
				questions := []any{map[string]any{"question": "Which format?", "options": []any{map[string]string{"label": "JSON", "description": "Use this."}, map[string]string{"label": "Markdown"}}}}
				home := t.TempDir()
				config := filepath.Join(home, ".claude")
				fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-auto-question.fixture.ts")
				ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				cmd.Dir = home
				cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AUTO_QUESTION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
				cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "loopbackFetchOrigin": upstream.URL, "cases": []any{}, "http": []any{map[string]any{"provider": provider, "questions": questions, "context": "Prefer JSON."}}}))
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				if err != nil {
					t.Fatal("original decision", err, stderr.String())
				}
				if !bytes.Contains(obj(out).Get("capabilities"), []byte(`"blocked":[]`)) {
					t.Fatal("unexpected Source capability", string(out))
				}
				var source []any
				_ = json.Unmarshal(obj(out).Get("http"), &source)
				mu.Lock()
				want := append([]wire(nil), requests...)
				requests = nil
				mu.Unlock()
				// An independent Go profile gets the same user record, rather than
				// reusing data produced by the original implementation.
				goHome := t.TempDir()
				goConfig := filepath.Join(goHome, ".claude")
				if err := os.MkdirAll(goConfig, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(goConfig, "settings.json"), []byte(`{"network":{"proxy":{"mode":"direct"}}}`), 0600); err != nil {
					t.Fatal(err)
				}
				store := storage.New(storage.Options{})
				upgrader, err := migrations.New(migrations.Options{Store: store})
				if err != nil {
					t.Fatal(err)
				}
				env := map[string]string{"HOME": goHome, "CLAUDE_CONFIG_DIR": goConfig, "DISABLE_TELEMETRY": "1"}
				settingService, err := settings.New(settings.Options{Store: store, HomeDir: goHome, ConfigDir: goConfig, Environment: func() map[string]string { return env }, EnsureMigrated: upgrader.SettingsCallback, ShellEnvironment: func(context.Context) (map[string]string, error) { return env, nil }, GitExecutable: "git"})
				if err != nil {
					t.Fatal(err)
				}
				providers, err := providerconfig.New(providerconfig.Options{Store: store, ConfigDir: goConfig, Settings: settingService})
				if err != nil {
					t.Fatal(err)
				}
				p := storage.Object{}
				_ = json.Unmarshal(raw(provider), &p)
				saved, err := providers.Add(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				var id string
				_ = json.Unmarshal(saved["id"], &id)
				proxy, err := providerproxytransport.New(providerproxytransport.Options{Provider: providers.ProviderForProxy, Settings: func(ctx context.Context) (protocol.JSONObject, error) {
					value, e := settingService.GetUserSettings(ctx)
					return obj(raw(value)), e
				}, Environment: func() map[string]string { return env }})
				if err != nil {
					t.Fatal(err)
				}
				decider, err := newDesktopAutoQuestionDecider(ServerOptions{Environment: env}, settingService, providers, nil, proxy)
				if err != nil {
					t.Fatal(err)
				}
				result, err := decider.Decide(ctx, autoquestion.DecisionInput{Questions: autoquestion.ParseQuestions(raw(map[string]any{"questions": questions})), Conversation: raw("Prefer JSON."), ProviderID: &id, SessionID: "private-fixture"})
				if err != nil {
					t.Fatal("Go production decision", err)
				}
				var actual any
				_ = json.Unmarshal(raw(result), &actual)
				if len(source) != 1 || !reflect.DeepEqual(actual, source[0]) {
					t.Fatalf("Source%s Go%s", raw(source), raw(actual))
				}
				mu.Lock()
				defer mu.Unlock()
				if !reflect.DeepEqual(requests, want) {
					t.Fatalf("actual upstream Source%s Go%s", raw(want), raw(requests))
				}
				for _, request := range requests {
					if strings.Contains(request.Headers["Authorization"], "proxy-managed") {
						t.Fatal("dummy credential reached real upstream")
					}
				}
			})
		}
	}
}
