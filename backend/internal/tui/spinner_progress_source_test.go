package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestActualOriginalREPLSpinnerStreamModeAndUTF16Counter(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual original REPL stream callback required")
	}
	rows := [][]json.RawMessage{
		{[]byte(`{"type":"stream_request_start"}`), []byte(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"actual-msg"}}}`), []byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`), []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"A😀\ud800"}}}`), []byte(`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`), []byte(`{"type":"stream_event","event":{"type":"message_delta"}}`), []byte(`{"type":"stream_event","event":{"type":"message_stop"}}`), []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"complete text is not another delta"}]}}`)},
		{[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`), []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"中文\udfff😀"}}}`), []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIGNATURE_DOES_NOT_COUNT"}}}`)},
		{[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"actual-use","name":"Read","input":{}}}}`), []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"中文😀\ud800\"}"}}}`), []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":9,"delta":{"type":"input_json_delta","partial_json":"\n unmatched original index still counts"}}}`), []byte(`{"type":"stream_event","event":{"type":"message_stop"}}`)},
	}
	for _, kind := range []string{"redacted_thinking", "server_tool_use", "web_search_tool_result", "code_execution_tool_result", "mcp_tool_use", "mcp_tool_result", "container_upload", "web_fetch_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result", "tool_search_tool_result", "compaction", "future_unrecognized_block"} {
		start, _ := json.Marshal(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": kind}}})
		rows = append(rows, []json.RawMessage{[]byte(`{"type":"stream_request_start"}`), start, []byte(`{"type":"stream_event","event":{"type":"future_unrecognized_event"}}`)})
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-spinner-events.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SPINNER_EVENTS_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Actual original handleMessageFromStream and REPL length callback", err, string(output))
	}
	var original struct {
		Results [][]struct {
			Mode          string
			Length, Added int
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &original); err != nil || len(original.Results) != len(rows) || len(original.Caps.Blocked) != 0 || len(original.Caps.SecureReads) != 0 {
		t.Fatal(err, string(output))
	}
	for index, messages := range rows {
		mode, length := "responding", 0
		for step, raw := range messages {
			message := presentationObject(raw)
			progress := TerminalSpinnerProgress{}
			if message.String("type") == "stream_request_start" {
				progress.Mode = "requesting"
			} else if message.String("type") == "stream_event" {
				var err error
				progress, _, err = TerminalSpinnerStreamProgress(message.Get("event"))
				if err != nil {
					t.Fatal(err, string(raw))
				}
			}
			if progress.Mode != "" {
				mode = progress.Mode
			}
			length += progress.AddedResponseUnits
			want := original.Results[index][step]
			if mode != want.Mode || length != want.Length || progress.AddedResponseUnits != want.Added {
				t.Fatalf("Original spinner stream profile%d step%d differs: Source%+v Native mode%s length%d added%d event%s", index, step, want, mode, length, progress.AddedResponseUnits, raw)
			}
		}
	}
}
