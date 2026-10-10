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
