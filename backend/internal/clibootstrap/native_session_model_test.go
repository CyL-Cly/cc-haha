package clibootstrap

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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func TestActualNativeSDKModelControlMatchesSourceHeadlessRequestsBreadcrumbsReplayAndSettings(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	for _, requested := range []string{"sonnet", "default", "arbitrary-concrete-model"} {
		t.Run(requested, func(t *testing.T) {
			var mu sync.Mutex
			requests := []json.RawMessage{}
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, data)
				round := len(requests)
				mu.Unlock()
				emitWorktreeModel(w, round, "", "", "ordinary complete")
			}))
			defer model.Close()
			home := t.TempDir()
			canonical, err := filepath.EvalSymlinks(home)
			if err != nil {
				t.Fatal(err)
			}
			home = canonical
			_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
			_ = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"model":"settings-model","enabledPlugins":{"sample@mkt":false}}`), 0600)
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-set-model.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_SET_MODEL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "TZ=UTC", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-fixture-key", "ANTHROPIC_BASE_URL=" + model.URL, "ANTHROPIC_DEFAULT_SONNET_MODEL=mapped-sonnet", "ANTHROPIC_DEFAULT_HAIKU_MODEL=mapped-haiku", "ANTHROPIC_MODEL=environment-model", "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_CODE_DISABLE_ATTACHMENTS=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=true", "ENABLE_TOOL_SEARCH=false", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "origin": model.URL, "requested": requested}))
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err = cmd.Run(); err != nil {
				t.Fatal(err, stderr.String(), stdout.String())
			}
			data, err := os.ReadFile(filepath.Join(home, "model-switch-result.json"))
			if err != nil {
				t.Fatal(err, stderr.String(), stdout.String())
			}
			var source struct {
				Code         int
				Requests     []protocol.JSONObject
				Histories    [][]protocol.JSONObject
				Replays      []protocol.JSONObject
				Controls     []protocol.JSONObject
				Capabilities struct{ Blocked []string }
			}
			if err = json.Unmarshal(data, &source); err != nil || source.Code != 0 || len(source.Requests) != 2 || len(source.Capabilities.Blocked) > 0 {
				t.Fatal(err, string(data), stderr.String())
			}
			f := newNativePluginFixture(t)
			settings := `{"model":"settings-model","enabledPlugins":{"sample@mkt":false}}`
			f.write(t, filepath.Join(f.config, "settings.json"), settings)
			f.n.Environment["ANTHROPIC_API_KEY"] = "offline-fixture-key"
			f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
			f.n.Environment["ANTHROPIC_MODEL"] = "environment-model"
			f.n.Environment["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "mapped-sonnet"
			f.n.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "mapped-haiku"
			f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			f.n.Environment["DISABLE_TELEMETRY"] = "1"
			f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
			f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "true"
			replays := []protocol.JSONObject{}
			output := func(line []byte) error {
				var v protocol.JSONObject
				if err := v.UnmarshalJSON(bytes.TrimSpace(line)); err != nil {
					return err
				}
				if v.String("type") == "user" && string(v.Get("isReplay")) == "true" {
					replays = append(replays, v)
				}
				return nil
			}
			m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: output, Arguments: PrintArguments{Model: "first-model", SystemPrompt: "Actual switch system.", Tools: []string{"Read"}, PermissionMode: "bypassPermissions", MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
			if err != nil {
				t.Fatal(err)
			}
			defer m.ShutdownSession(context.Background())
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			input := func(i int) protocol.JSONObject {
				return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": fmtTurn(i)})})
			}
			if err = m.RunTurn(ctx, input(1)); err != nil {
				t.Fatal(err)
			}
			if err = m.SetSessionModel(ctx, requested); err != nil {
				t.Fatal(err)
			}
			actualModel, err := m.SessionModel(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if actualModel != source.Requests[1].String("model") {
				t.Fatal("next model", actualModel, string(rawMain(source.Requests[1])))
			}
			var history []protocol.JSONObject
			if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error { history = m.Controller.History(); return nil }); err != nil {
				t.Fatal(err)
			}
			bread := []string{}
			for _, message := range history {
				var row protocol.JSONObject
				_ = row.UnmarshalJSON(message.Get("message"))
				var content string
				_ = json.Unmarshal(row.Get("content"), &content)
				if strings.Contains(content, "<command-name>/model") || strings.Contains(content, "<local-command-stdout>Set model") {
					bread = append(bread, content)
				}
			}
			sourceBread := []string{}
			for _, message := range source.Histories[1] {
				var row protocol.JSONObject
				_ = row.UnmarshalJSON(message.Get("message"))
				var content string
				_ = json.Unmarshal(row.Get("content"), &content)
				if strings.Contains(content, "<command-name>/model") || strings.Contains(content, "<local-command-stdout>Set model") {
					sourceBread = append(sourceBread, content)
				}
			}
			if !reflect.DeepEqual(bread, sourceBread) {
				t.Fatal("breadcrumbs", bread, sourceBread)
			}
			modelReplays := []protocol.JSONObject{}
			for _, replay := range replays {
				if bytes.Contains(replay.Get("message"), []byte("local-command-stdout")) {
					modelReplays = append(modelReplays, replay)
				}
			}
			if len(modelReplays) != 1 || len(source.Replays) != 1 || !planSourcesEqual(modelReplays[0].Get("message"), source.Replays[0].Get("message")) || string(modelReplays[0].Get("parent_tool_use_id")) != "null" {
				t.Fatal("actual replay", string(rawMain(replays)), string(rawMain(source.Replays)))
			}
			if err = m.RunTurn(ctx, input(2)); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			count := len(requests)
			last := append([]byte(nil), requests[len(requests)-1]...)
			mu.Unlock()
			if count != 4 || !bytes.Contains(last, []byte("ordinary turn 1")) || !bytes.Contains(last, []byte("ordinary turn 2")) {
				t.Fatal("shared actual HTTP history", count, string(last))
			}
			actualSettings, _ := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if string(actualSettings) != settings {
				t.Fatal("model control wrote global settings", string(actualSettings))
			}
		})
	}
}
func fmtTurn(i int) string {
	if i == 1 {
		return "ordinary turn 1"
	}
	return "ordinary turn 2"
}

func TestNativeSessionAccountingUsesProcessContextThroughFreshAndColdRequests(t *testing.T) {
	for _, tc := range []struct {
		name, model, windows, disabled, globalOutput, providerOutput string
		window, requestOutput                                        float64
	}{
		{"configured-1m-global-output", "fixture-model", `{"fixture-model":1000000}`, "", "2048", "4096", 1000000, 2048},
		{"configured-window-before-marker", "fixture-model[1m]", `{"fixture-model":64000}`, "", "4096", "2048", 64000, 4096},
		{"disabled-extended-context", "fixture-model", `{"fixture-model":1000000}`, "1", "", "1024", 200000, 1024},
		{"unconfigured-original-default", "fixture-model", "", "", "", "", 200000, 32000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
			var mu sync.Mutex
			requests := []protocol.JSONObject{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
					t.Errorf("unexpected model transport %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				data, err := io.ReadAll(r.Body)
				var body protocol.JSONObject
				if err != nil || body.UnmarshalJSON(data) != nil {
					t.Error("invalid actual request body", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				mu.Lock()
				requests = append(requests, body)
				round := len(requests)
				mu.Unlock()
				emitWorktreeModel(w, round, "", "", "actual process-ledger result")
			}))
			defer peer.Close()
			baseEnv := map[string]string{}
			for k, v := range f.n.Environment {
				baseEnv[k] = v
			}
			baseEnv["ANTHROPIC_API_KEY"] = "fake-only-loopback"
			baseEnv["ANTHROPIC_BASE_URL"] = peer.URL
			baseEnv["ANTHROPIC_MODEL"] = tc.model
			baseEnv["CLAUDE_CODE_MAX_RETRIES"] = "0"
			baseEnv["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "true"
			for key, value := range map[string]string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": tc.windows, "CLAUDE_CODE_DISABLE_1M_CONTEXT": tc.disabled, "CLAUDE_CODE_MAX_OUTPUT_TOKENS": tc.globalOutput, "CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS": tc.providerOutput} {
				if value != "" {
					baseEnv[key] = value
				}
			}
			filtered, err := sdkhost.NativePrintRequestEnvironment(baseEnv)
			if err != nil {
				t.Fatal(err)
			}
			if _, present := filtered["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"]; present {
				t.Fatal("local context window leaked into the filtered request environment")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			const sid = "90471151-1933-493f-8373-fb2fcd40e731"
			for phase, name := range []string{"fresh", "cold-resume"} {
				// Reconstruct NativeBoundary/MainPrint in this test process; reuse only private disk state.
				env := map[string]string{}
				for k, v := range baseEnv {
					env[k] = v
				}
				n, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: f.cwd, Platform: "darwin", ManagedRoot: f.managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
				if err != nil {
					t.Fatal(err)
				}
				n.Temp, n.RunSecurity = f.n.Temp, f.n.RunSecurity
				bindCommandPrefetchFixture(t, n)
				args := PrintArguments{Model: tc.model, SessionID: sid, SystemPrompt: "Actual private native accounting.", Tools: []string{"Read"}, PermissionMode: "bypassPermissions", MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}
				if phase == 1 {
					args.SessionID, args.Resume = "", sid
				}
				m, err := NewMainPrint(MainPrintOptions{Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: args})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(m.Close)
				if err = m.StartSession(ctx); err != nil {
					t.Fatal(name, err)
				}
				input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Process accounting " + name})})
				if err = m.RunTurn(ctx, input); err != nil {
					t.Fatal(name, err)
				}
				usage, err := m.ReadSessionUsage(ctx)
				if err != nil {
					t.Fatal(name, err)
				}
				var models []struct {
					Model           string  `json:"model"`
					ContextWindow   float64 `json:"contextWindow"`
					MaxOutputTokens float64 `json:"maxOutputTokens"`
				}
				if err = json.Unmarshal(usage.Get("models"), &models); err != nil || len(models) != 1 || models[0].Model != tc.model {
					t.Fatalf("%s actual usage schema/model: %s, error %v", name, rawMain(usage), err)
				}
				if models[0].ContextWindow != tc.window || models[0].MaxOutputTokens != 32000 {
					t.Fatalf("%s actual process ledger: contextWindow=%v want=%v defaultMaxOutputTokens=%v want=32000", name, models[0].ContextWindow, tc.window, models[0].MaxOutputTokens)
				}
				mu.Lock()
				captured := append([]protocol.JSONObject{}, requests...)
				mu.Unlock()
				var maxTokens float64
				if len(captured) != phase+1 {
					t.Fatalf("%s usage control generated extra inference requests: %d", name, len(captured))
				}
				if err = json.Unmarshal(captured[phase].Get("max_tokens"), &maxTokens); err != nil || maxTokens != tc.requestOutput {
					t.Fatalf("%s actual output priority: max_tokens=%v want=%v error=%v", name, maxTokens, tc.requestOutput, err)
				}
				if err = m.ShutdownSession(ctx); err != nil {
					t.Fatal(name, err)
				}
			}
		})
	}
}
