package application

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
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPublicCLIProducesDeniedCatalogAndRawCustomAgentThenColdResume(t *testing.T) {
	for _, scenario := range []string{"deny", "custom-agent"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			var forbidden string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				round := calls.Add(1)
				var request protocol.JSONObject
				_ = request.UnmarshalJSON(data)
				if scenario == "deny" {
					var tools []protocol.JSONObject
					_ = json.Unmarshal(request.Get("tools"), &tools)
					read, write := false, false
					for _, tool := range tools {
						read = read || tool.String("name") == "Read"
						write = write || tool.String("name") == "Write"
					}
					if !read || write {
						t.Error("CLI deny rule did not reach actual request tool catalog", read, write)
					}
				} else {
					if !bytes.Contains(data, []byte("ACTUAL_CLI_FLAG_AGENT_SYSTEM_中文")) {
						t.Error("raw CLI agent did not become actual main prompt")
					}
					var tools []protocol.JSONObject
					_ = json.Unmarshal(request.Get("tools"), &tools)
					for _, tool := range tools {
						if tool.String("name") != "Read" && tool.String("name") != "Write" {
							t.Error("main CLI invocation tool catalog changed by agent definition", tool.String("name"))
						}
					}
				}
				if scenario == "deny" && round%2 == 1 {
					response(w, "forbidden-cli-write", "Write", map[string]any{"file_path": forbidden, "content": "MUST_NOT_EXIST"}, "")
					return
				}
				if scenario == "deny" && !bytes.Contains(data, []byte(`"is_error":true`)) {
					t.Error("forbidden model tool was not returned as original error feedback")
				}
				response(w, "public-cli-rules-agent", "", nil, "CLI_FLAG_TASK_COMPLETED")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			forbidden = filepath.Join(o.WorkDir, "forbidden-write.txt")
			path := filepath.Join(o.WorkDir, "cli-read.txt")
			if err := os.WriteFile(path, []byte("actual private input"), 0600); err != nil {
				t.Fatal(err)
			}
			sid := "fe210ac4-5364-4b5e-a0b8-e351b70e0a17"
			flags := []string{"--print", "--model", "fixture-model", "--thinking", "disabled", "--session-id", sid, "--tools", "Read,Write", "--permission-mode", "bypassPermissions", "--mcp-debug"}
			if scenario == "deny" {
				flags = append(flags, "--disallowedTools", "Write")
			} else {
				flags = append(flags, "--agents", `{"cli-worker":{"description":"Actual CLI custom Agent","prompt":"ACTUAL_CLI_FLAG_AGENT_SYSTEM_中文","tools":["Read"],"model":"inherit"}}`, "--agent", "cli-worker")
			}
			// Real public argument producer and normal RunCommand/Session/MainPrint.
			for _, cold := range []bool{false, true} {
				argv := append([]string{}, flags...)
				if cold {
					for index, value := range argv {
						if value == "--session-id" {
							argv[index] = "--resume"
							break
						}
					}
				}
				argv = append(argv, "--", "Complete the actual private CLI task")
				command, _, err := ParseCommand(argv, o.Version, o.WorkDir, t.TempDir(), o.Environment)
				if err != nil {
					t.Fatal("public CLI parse", err)
				}
				var output bytes.Buffer
				code, err := RunCommand(context.Background(), command, o, strings.NewReader(""), &output, nil)
				if err != nil || code != 0 || !strings.Contains(output.String(), "CLI_FLAG_TASK_COMPLETED") {
					t.Fatal("actual CLI task/cold resume", cold, code, err, output.String())
				}
			}
			expected := int32(2)
			if scenario == "deny" {
				expected = 4
				if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
					t.Fatal("CLI deny permitted physical Write", err)
				}
			}
			if calls.Load() != expected {
				t.Fatal("CLI command repeated or skipped task", calls.Load())
			}
		})
	}
}
