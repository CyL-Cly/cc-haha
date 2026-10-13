package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPublicCLIJSONAgentSpawnsRestrictedPhysicalReadChildThenColdResumes(t *testing.T) {
	var parents, children atomic.Int32
	var path string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		request := obj(data)
		if bytes.Contains(request.Get("system"), []byte("ACTUAL_CLI_CHILD_PRIVATE_PROMPT")) {
			round := children.Add(1)
			var tools []protocol.JSONObject
			_ = json.Unmarshal(request.Get("tools"), &tools)
			for _, tool := range tools {
				if tool.String("name") != "Read" {
					t.Error("CLI JSON child tools were not enforced", tool.String("name"))
				}
			}
			if round%2 == 1 {
				response(w, "cli-child-read", "Read", map[string]any{"file_path": path}, "")
			} else {
				if !bytes.Contains(data, []byte("CLI_CHILD_PHYSICAL_READ_BODY")) {
					t.Error("physical child result did not reach same child inference")
				}
				response(w, "cli-child-done", "", nil, "CLI_CHILD_READ_COMPLETED")
			}
			return
		}
		round := parents.Add(1)
		if round%2 == 1 {
			response(w, "cli-parent-agent", "Agent", map[string]any{"description": "Execute private CLI JSON agent", "prompt": "Read the private input file and finish", "subagent_type": "cli-reader"}, "")
		} else {
			if !bytes.Contains(data, []byte("CLI_CHILD_READ_COMPLETED")) {
				t.Error("child completion did not reach real parent")
			}
			response(w, "cli-parent-done", "", nil, "CLI_JSON_AGENT_TASK_DONE")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	path = filepath.Join(o.WorkDir, "child-read.txt")
	_ = os.WriteFile(path, []byte("CLI_CHILD_PHYSICAL_READ_BODY"), 0600)
	for _, cold := range []bool{false, true} {
		flag := "--session-id"
		if cold {
			flag = "--resume"
		}
		argv := []string{"--print", "--model", "fixture-model", "--tools", "Agent,Read,Write", "--permission-mode", "bypassPermissions", "--system-prompt", "Use the private cli-reader Agent to execute the task", flag, o.Arguments.SessionID, "--agents", `{"cli-reader":{"description":"Private CLI reader","prompt":"ACTUAL_CLI_CHILD_PRIVATE_PROMPT","tools":["Read"],"model":"inherit"}}`, "--", "Use cli-reader to read the private file"}
		command, _, err := ParseCommand(argv, o.Version, o.WorkDir, t.TempDir(), o.Environment)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		code, err := RunCommand(context.Background(), command, o, strings.NewReader(""), &output, nil)
		if err != nil || code != 0 || !strings.Contains(output.String(), "CLI_JSON_AGENT_TASK_DONE") {
			t.Fatal(cold, code, err, output.String())
		}
	}
	if parents.Load() != 4 || children.Load() != 4 {
		t.Fatal("CLI Agent startup/cold-resume duplicated or skipped work", parents.Load(), children.Load())
	}
	subagents, _ := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", o.Arguments.SessionID, "subagents", "agent-*.jsonl"))
	if len(subagents) != 2 {
		t.Fatal("independent actual CLI children did not persist sidechains", subagents)
	}
}
