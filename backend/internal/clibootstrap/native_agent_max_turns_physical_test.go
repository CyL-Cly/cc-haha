package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This starts the normal SDK stdin consumer, not a manually populated child
// Host. A parsed initialize definition must reach two independent Agent calls,
// perform real filesystem tools, stop at its own limit, and release its slot.
func TestActualSDKAgentMaxTurnsStopsAfterPhysicalToolsAndAllowsNextParentTurn(t *testing.T) {
	for _, limit := range []int{1, 2} {
		t.Run(string(rune('0'+limit)), func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"maxConcurrentSubagents":1}`)
			f.n.Environment["ANTHROPIC_API_KEY"] = "fake-agent-max-turns"
			f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
			f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
			paths := []string{filepath.Join(f.cwd, "first-limited-agent.txt"), filepath.Join(f.cwd, "second-limited-agent.txt")}
			const marker = "PHYSICAL_LIMITED_AGENT_WRITE"
			var mu sync.Mutex
			parentRounds, childRounds := 0, 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var request protocol.JSONObject
				if err = request.UnmarshalJSON(data); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				for _, message := range taskBlocks(request.Get("messages")) {
					for _, block := range taskBlocks(message.Get("content")) {
						if block.String("type") == "tool_result" && string(block.Get("is_error")) == "true" {
							t.Errorf("real limited Agent tool failed: %s", rawMain(block))
						}
					}
				}
				if strings.Contains(string(request.Get("system")), "ACTUAL_SDK_LIMITED_SYSTEM") {
					childRounds++
					index := (parentRounds - 1) / 2
					if index < 0 || index >= len(paths) {
						t.Error("child outlived admitted parent turn", parentRounds)
						http.Error(w, "unexpected child", http.StatusBadRequest)
						return
					}
					if (childRounds-1)%limit == 0 {
						emitNativeTaskTool(w, "limited-write", "Write", map[string]any{"file_path": paths[index], "content": marker})
					} else {
						if !bytes.Contains(data, []byte("limited-write")) {
							t.Error("child Read lost preceding actual Write result")
						}
						emitNativeTaskTool(w, "limited-read", "Read", map[string]any{"file_path": paths[index]})
					}
					return
				}
				parentRounds++
				if parentRounds > 4 {
					t.Error("child limit failed to terminate parent continuation", parentRounds)
					http.Error(w, "unexpected parent", http.StatusBadRequest)
					return
				}
				if parentRounds%2 == 1 {
					emitNativeTaskTool(w, "parent-limited-agent", "Agent", map[string]any{"description": "Run the limited SDK worker", "prompt": "Write the private marker, then read it", "subagent_type": "limited-worker"})
					return
				}
				index := parentRounds/2 - 1
				physical, err := os.ReadFile(paths[index])
				if err != nil || string(physical) != marker {
					t.Errorf("parent continued before physical child Write completed: %q %v", physical, err)
				}
				if childRounds != (index+1)*limit {
					t.Error("wrong child model limit", childRounds, limit, index)
				}
				emitPlanFixture(w, parentRounds, "", "PARENT_LIMITED_AGENT_COMPLETE")
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			input, writer := io.Pipe()
			defer input.Close()
			defer writer.Close()
			var output, diagnostics bytes.Buffer
			results := make(chan protocol.JSONObject, 2)
			disabled := false
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", AllowedTools: []string{"Write", "Read"}, MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: input, Output: &output, Error: &diagnostics, OnOutput: func(data []byte) error {
				output.Write(data)
				var frame protocol.JSONObject
				if frame.UnmarshalJSON(data) == nil && frame.String("type") == "result" {
					results <- frame
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			done := make(chan struct{})
			go func() { defer close(done); _, err := m.Run(ctx); finished <- err }()
			defer func() {
				cancel()
				_ = writer.Close()
				_ = input.Close()
				select {
				case <-done:
				case <-time.After(30 * time.Second):
					t.Error("actual SDK runtime did not join during test cleanup")
				}
			}()
			write := func(frame any) {
				t.Helper()
				line := append(rawMain(frame), '\n')
				if _, err := writer.Write(line); err != nil {
					t.Fatal("SDK stdin failed", err)
				}
			}
			write(map[string]any{"type": "control_request", "request_id": "limited-worker-init", "request": map[string]any{"subtype": "initialize", "agents": map[string]any{"limited-worker": map[string]any{"description": "Limited actual filesystem worker", "prompt": "ACTUAL_SDK_LIMITED_SYSTEM", "tools": []string{"Write", "Read"}, "model": "inherit", "maxTurns": limit}}}})
			for turn := 0; turn < 2; turn++ {
				write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Use the limited-worker Agent to write and read the private marker"}})
				select {
				case result := <-results:
					if result.String("subtype") != "success" || string(result.Get("is_error")) != "false" {
						t.Fatal("child maximum leaked into parent result", string(rawMain(result)))
					}
				case err := <-finished:
					t.Fatal("SDK session ended before parent result", err, diagnostics.String())
				case <-ctx.Done():
					t.Fatal("real limited child did not return", ctx.Err())
				}
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-finished:
				if err != nil {
					t.Fatal(err, diagnostics.String())
				}
			case <-ctx.Done():
				t.Fatal("stdin EOF did not drain native children", ctx.Err())
			}
			select {
			case <-m.Owner.Done():
			default:
				t.Fatal("ordinary SDK runtime owner was not closed")
			}
			mu.Lock()
			if parentRounds != 4 || childRounds != 2*limit {
				t.Error("unexpected physical request cardinality", parentRounds, childRounds)
			}
			mu.Unlock()
			paths, err = filepath.Glob(filepath.Join(f.config, "projects", "*", m.Controller.SessionID(), "subagents", "agent-*.jsonl"))
			if err != nil || len(paths) != 2 {
				t.Fatal("distinct children must retain two physical sidechains", paths, err)
			}
			for _, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Contains(data, []byte(`"isSidechain":true`)) || !bytes.Contains(data, []byte(marker)) {
					t.Fatal("child physical tool/transcript receipt missing", path, string(data), err)
				}
				if bytes.Contains(data, []byte("max_turns_reached")) || bytes.Contains(data, []byte("error_max_turns")) {
					t.Fatal("Source Agent limit attachment must not become durable sidechain content", string(data))
				}
				var rows []protocol.JSONObject
				for _, line := range bytes.Split(data, []byte{'\n'}) {
					var row protocol.JSONObject
					if row.UnmarshalJSON(line) == nil && row.String("type") == "assistant" {
						rows = append(rows, row)
					}
				}
				if len(rows) != limit {
					t.Fatal("sidechain recorded tool replay or lost child model response", len(rows), limit)
				}
			}
			var frames []protocol.JSONObject
			decoder := json.NewDecoder(&output)
			for {
				var frame protocol.JSONObject
				if err = decoder.Decode(&frame); err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				frames = append(frames, frame)
			}
			resultCount, initializeCount := 0, 0
			for _, frame := range frames {
				if frame.String("type") == "result" {
					resultCount++
				}
				if frame.String("type") == "control_response" {
					response, _ := frame.Object("response")
					if response.String("request_id") == "limited-worker-init" && response.String("subtype") == "success" {
						initializeCount++
					}
				}
				if frame.String("type") == "result" && frame.String("subtype") == "error_max_turns" {
					t.Fatal("child terminal result escaped into parent SDK output")
				}
			}
			if resultCount != 2 || initializeCount != 1 {
				t.Fatal("original SDK initialize/parent result consumer cardinality", initializeCount, resultCount)
			}
		})
	}
}
