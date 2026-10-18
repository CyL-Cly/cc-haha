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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSDKInitializeFalsyOptionalProducerValuesAndTruthyPromptSourceAndMainPrintResult(t *testing.T) {
	requests := []map[string]any{
		{"subtype": "initialize"},
		{"subtype": "initialize", "promptSuggestions": false, "agentProgressSummaries": false},
		{"subtype": "initialize", "promptSuggestions": nil, "agentProgressSummaries": nil},
		{"subtype": "initialize", "promptSuggestions": 0, "agentProgressSummaries": 0},
		{"subtype": "initialize", "promptSuggestions": "", "agentProgressSummaries": ""},
		{"subtype": "initialize", "promptSuggestions": true},
		{"subtype": "initialize", "promptSuggestions": 1},
		{"subtype": "initialize", "promptSuggestions": "false"},
		{"subtype": "initialize", "promptSuggestions": []any{}},
		{"subtype": "initialize", "promptSuggestions": map[string]any{}},
	}
	names := []string{"omitted", "false", "null", "zero", "empty-string", "true", "one", "nonempty-string", "empty-array", "empty-object"}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source Bun required")
	}
	fixture, err := filepath.Abs("../../testdata/native-sdk-initialize-options.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	preload, err := filepath.Abs("../../../preload")
	if err != nil {
		t.Fatal(err)
	}
	sandbox, err := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", "--preload", preload, fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_OPTIONS_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "ANTHROPIC_API_KEY=fake-disabled-producers", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"requests": requests, "initial": "fixture-model", "override": "fixture-model"}))
	var sourceErrors bytes.Buffer
	cmd.Stderr = &sourceErrors
	data, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source initialize", err, sourceErrors.String())
	}
	var source struct {
		Rows []struct {
			Output  []protocol.JSONObject
			Options protocol.JSONObject
		}
		Capabilities protocol.JSONObject
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(requests) {
		t.Fatal(err, string(data))
	}
	var blocked []string
	if err = json.Unmarshal(source.Capabilities.Get("blocked"), &blocked); err != nil || len(blocked) != 0 {
		t.Fatal("Source capability escape", err, blocked)
	}
	for index, request := range requests {
		t.Run(names[index], func(t *testing.T) {
			row := source.Rows[index]
			if len(row.Output) != 1 || row.Output[0].String("type") != "control_response" {
				t.Fatal("original initialize result", string(rawMain(row.Output)))
			}
			sourceResponse, _ := row.Output[0].Object("response")
			if sourceResponse.String("subtype") != "success" || sourceResponse.String("request_id") != "agents-request" {
				t.Fatal("original initialize acknowledgement", string(rawMain(sourceResponse)))
			}
			if value, present := request["promptSuggestions"]; present && !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(row.Options.Get("promptSuggestions")), protocol.StringifyValidatedJavaScriptJSON(rawMain(value))) {
				t.Fatal("Source did not retain exact present runtime value", string(rawMain(row.Options)))
			}
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusOK)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if r.Method != http.MethodPost || !bytes.Contains(body, []byte("DISABLED_OPTION_ACTUAL_USER")) {
					t.Error("not the actual ordinary query", r.Method, string(body))
				}
				if calls.Add(1) != 1 {
					t.Error("disabled producer started an extra model consumer")
				}
				emitWorktreeModel(w, 1, "", "", "DISABLED_OPTION_ACTUAL_RESULT")
			}))
			defer peer.Close()
			f := newNativePluginFixture(t)
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			f.n.Environment["ANTHROPIC_API_KEY"] = "fake-disabled-producers"
			f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
			f.n.Environment["CLAUDE_CODE_DISABLE_FAST_MODE"] = "1"
			f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			var input, output, diagnostics bytes.Buffer
			input.Write(rawMain(map[string]any{"type": "control_request", "request_id": "disabled-init", "request": request}))
			input.WriteByte('\n')
			input.WriteString(`{"type":"user","message":{"role":"user","content":"DISABLED_OPTION_ACTUAL_USER"}}` + "\n")
			disabled := false
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Complete the actual request.", MaxTurns: 6, PermissionMode: "default", Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: &input, Output: &output, Error: &diagnostics})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			if _, err = m.Run(ctx); err != nil {
				t.Fatal("actual initialize-to-query consumer", err, diagnostics.String())
			}
			decoder := json.NewDecoder(&output)
			ack, result := -1, -1
			for at := 0; ; at++ {
				var event protocol.JSONObject
				err = decoder.Decode(&event)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if event.String("type") == "control_response" {
					response, _ := event.Object("response")
					if response.String("request_id") == "disabled-init" && response.String("subtype") == sourceResponse.String("subtype") {
						ack = at
					}
				}
				if event.String("type") == "result" {
					if string(event.Get("is_error")) != "false" || event.String("result") != "DISABLED_OPTION_ACTUAL_RESULT" {
						t.Fatal("actual result changed", string(rawMain(event)))
					}
					result = at
				}
				if event.String("type") == "prompt_suggestion" {
					t.Fatal("disabled suggestion emitted", string(rawMain(event)))
				}
			}
			if ack < 0 || result <= ack || calls.Load() != 1 {
				t.Fatal("initialize ACK then actual SDK result missing or extra model", ack, result, calls.Load())
			}
		})
	}
}
