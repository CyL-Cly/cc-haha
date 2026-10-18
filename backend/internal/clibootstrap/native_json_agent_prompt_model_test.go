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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualJSONAgentRawPromptReachesCLIAndSDKChildPhysicalRead(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	definition := objMain(json.RawMessage(`{"description":"Actual raw JSON worker","prompt":" \tJSON_AGENT_RAW:\ud800middle\udfffend\ufeff ","tools":["Read"],"model":"inherit"}`))
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-json.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AGENT_JSON_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "cases": []map[string]any{{"name": "raw-worker", "definition": definition, "memory": false}}, "runtimePrompts": true}))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source JSON getSystemPrompt", err, string(output))
	}
	var source struct {
		RuntimePrompts []json.RawMessage
		Capabilities   struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.RuntimePrompts) != 1 || len(source.Capabilities.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	want, err := protocol.DecodeJavaScriptJSONString(source.RuntimePrompts[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, producer := range []string{"cli", "sdk"} {
		t.Run(producer, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			physical := filepath.Join(f.cwd, "json-agent-physical.txt")
			f.write(t, physical, "JSON_AGENT_ACTUAL_READ_BYTES")
			f.n.Environment["ANTHROPIC_API_KEY"] = "fake-json-agent-key"
			f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
			f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
			var mu sync.Mutex
			parent, child := 0, 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				body := objMain(data)
				mu.Lock()
				defer mu.Unlock()
				for _, message := range taskBlocks(body.Get("messages")) {
					for _, block := range taskBlocks(message.Get("content")) {
						if block.String("type") == "tool_result" && string(block.Get("is_error")) == "true" {
							t.Error("Agent actual tool failure", string(block.Get("content")))
						}
					}
				}
				if bytes.Contains(body.Get("system"), []byte("JSON_AGENT_RAW:")) {
					child++
					matched := false
					for _, block := range taskBlocks(body.Get("system")) {
						units, err := protocol.DecodeJavaScriptJSONString(block.Get("text"))
						separator, _ := protocol.DecodeJavaScriptJSONString(quoteMain("\n\nNotes:\n"))
						for i := 0; i+len(separator) <= len(units); i++ {
							if reflect.DeepEqual(units[i:i+len(separator)], separator) {
								units = units[:i]
								break
							}
						}
						if err == nil && reflect.DeepEqual(units, want) {
							matched = true
						}
					}
					if !matched {
						t.Error("actual child model prompt differs from Source getSystemPrompt UTF16 units")
					}
					if child == 1 {
						emitNativeTaskTool(w, "json-child-read", "Read", map[string]any{"file_path": physical})
					} else {
						if !bytes.Contains(body.Get("messages"), []byte("JSON_AGENT_ACTUAL_READ_BYTES")) {
							t.Error("actual child Read result absent")
						}
						emitPlanFixture(w, child, "", "JSON_CHILD_DONE")
					}
				} else {
					parent++
					if parent == 1 {
						emitNativeTaskTool(w, "json-parent-agent", "Agent", map[string]any{"description": "Run actual JSON Agent", "prompt": "Read physical private input", "subagent_type": "raw-worker"})
					} else {
						if !bytes.Contains(body.Get("messages"), []byte("JSON_CHILD_DONE")) {
							t.Error("parent lost actual child result")
						}
						emitPlanFixture(w, parent, "", "JSON_PARENT_DONE")
					}
				}
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			var input, stdout, diagnostics bytes.Buffer
			definitions := protocol.NewJSONObject(protocol.JSONField{Name: "raw-worker", Value: rawMain(definition)})
			disabled := false
			arguments := PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual JSON Agent parent", Tools: []string{"Agent,Read"}, Sources: DefaultState(), MaxTurns: 8}
			if producer == "cli" {
				arguments.AgentsJSON = string(rawMain(definitions))
			} else {
				input.Write(rawMain(map[string]any{"type": "control_request", "request_id": "raw-json-agent", "request": map[string]any{"subtype": "initialize", "agents": definitions}}))
				input.WriteByte('\n')
			}
			input.Write(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Use raw-worker to read the private file"}}))
			input.WriteByte('\n')
			m, err := NewMainPrint(MainPrintOptions{Arguments: arguments, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "fixture", Input: &input, Output: &stdout, Error: &diagnostics})
			if err != nil {
				t.Fatal(err)
			}
			_, runErr := m.Run(ctx)
			closeErr := m.ShutdownSession(context.Background())
			if runErr != nil || closeErr != nil {
				t.Fatal(runErr, closeErr, diagnostics.String(), stdout.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if parent != 2 || child != 2 || !strings.Contains(stdout.String(), "JSON_PARENT_DONE") {
				t.Fatal("actual request/result counts", parent, child, stdout.String())
			}
		})
	}
}
