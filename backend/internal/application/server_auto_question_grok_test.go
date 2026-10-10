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
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/autoquestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestDesktopAutomaticGrokQuestionUsesOriginalFetchAndStreamContract(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	for _, status := range []int{200, 400, 422, 403} {
		for _, account := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/account%v", status, account), func(t *testing.T) {
				type request struct {
					Body    any
					Headers map[string]string
				}
				var mu sync.Mutex
				requests := []request{}
				answer := `{"answers":[{"questionIndex":0,"optionLabels":["JSON"]}]}`
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body any
					_ = json.NewDecoder(r.Body).Decode(&body)
					headers := map[string]string{}
					for _, name := range []string{"Content-Type", "Authorization", "X-Grok-Model-Override", "Anthropic-Version", "Accept", "Accept-Encoding", "User-Agent", "Connection", "X-Xai-Token-Auth", "X-Grok-Client-Version", "X-Grok-Client-Mode", "X-Grok-Client-Identifier", "X-Authenticateresponse"} {
						headers[name] = r.Header.Get(name)
					}
					mu.Lock()
					requests = append(requests, request{body, headers})
					mu.Unlock()
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(status)
					if status != 200 {
						_, _ = w.Write([]byte(`{"error":"private rejection"}`))
						return
					}
					completed := map[string]any{"id": "private", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": answer}}}, map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": "this second text must not be concatenated to the choice JSON"}}}}, "usage": map[string]int{"input_tokens": 9, "output_tokens": 3}}
					fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", raw(map[string]any{"type": "response.completed", "response": completed}))
				}))
				defer upstream.Close()
				questions := autoquestion.ParseQuestions([]byte(`{"questions":[{"question":"Which format?","options":[{"label":"JSON"},{"label":"Markdown"}]}]}`))
				body := map[string]any{"model": "grok-large", "max_tokens": 512, "system": "You are a constrained choice assistant. Return only the requested JSON object. Never invent options.", "messages": []any{map[string]any{"role": "user", "content": autoquestion.BuildPrompt(questions, raw("Prefer JSON."))}}}
				tokens := map[string]any{"accessToken": "fake-grok-question-token", "refreshToken": "fake-refresh", "expiresAt": time.Now().Add(time.Hour).UnixMilli()}
				if account {
					tokens["accountId"] = "fake-account"
				}
				home := t.TempDir()
				config := filepath.Join(home, ".claude")
				fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-auto-question-grok.fixture.ts")
				ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				cmd.Dir = home
				cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "PATH=/usr/bin:/bin", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file"}
				cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "endpoint": upstream.URL, "tokens": tokens, "body": body}))
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				if err != nil {
					t.Fatal("actual Source", err, stderr.String())
				}
				if obj(out).Get("error") != nil || !bytes.Contains(obj(out).Get("capabilities"), []byte(`"blocked":[]`)) {
					t.Fatal(string(out))
				}
				mu.Lock()
				want := append([]request(nil), requests...)
				requests = nil
				mu.Unlock()
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
				if err := os.MkdirAll(filepath.Join(goConfig, "cc-haha"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(goConfig, "cc-haha/providers.json"), []byte(`{"schemaVersion":6,"activeId":null,"providers":[],"officialProviderModels":{"grok-official":{"main":"grok-large","haiku":"grok-large","sonnet":"grok-large","opus":"grok-large"}}}`), 0600); err != nil {
					t.Fatal(err)
				}
				oauth, err := desktopoauth.New(desktopoauth.Options{Home: goHome, ConfigDir: goConfig, Platform: "linux", Store: store, Environment: func() map[string]string { return env }})
				if err != nil {
					t.Fatal(err)
				}
				defer oauth.Close()
				if err := oauth.Grok().SaveTokens(ctx, obj(raw(tokens))); err != nil {
					t.Fatal(err)
				}
				decider, err := newDesktopAutoQuestionDecider(ServerOptions{Environment: env}, settingService, providers, oauth, nil)
				if err != nil {
					t.Fatal(err)
				}
				resolve := decider.Resolve
				decider.Resolve = func(ctx context.Context, id *string, sid string) (*autoquestion.Target, error) {
					target, e := resolve(ctx, id, sid)
					if target != nil {
						target.URL = upstream.URL
						target.Client = upstream.Client()
					}
					return target, e
				}
				id := "grok-official"
				got, err := decider.Decide(ctx, autoquestion.DecisionInput{Questions: questions, Conversation: raw("Prefer JSON."), ProviderID: &id, SessionID: "private-grok"})
				if err != nil {
					t.Fatal("Go actual official resolver", err)
				}
				var wantAnswer any
				_ = json.Unmarshal(obj(out).Get("answer"), &wantAnswer)
				if status == 200 {
					if got == nil || got.String("Which format?") != "JSON" || wantAnswer != answer {
						t.Fatalf("Source%v Go%v", wantAnswer, got)
					}
				} else if got != nil || wantAnswer != nil {
					t.Fatal(wantAnswer, got)
				}
				mu.Lock()
				defer mu.Unlock()
				if len(requests) != 1 || !reflect.DeepEqual(requests, want) {
					t.Fatalf("physical Source%s Go%s", raw(want), raw(requests))
				}
				// Drain/close happened inside the real request path; no test SSE
				// converter or token callback is substituted on the Go side.
			})
		}
	}
}
