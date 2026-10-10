package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestPublicPrintJSONActualOriginalRetentionAndFormatting(t *testing.T) {
	result := json.RawMessage(`{"type":"result","subtype":"success","result":"<tag>\ud800\u2028","is_error":false}`)
	base := []json.RawMessage{json.RawMessage(`{"type":"system","subtype":"init","opaque":{"second":2,"first":1}}`), json.RawMessage(`{"type":"assistant","message":{"content":[{"type":"text","text":"physical result"}]}}`), result}
	types := []string{"control_response", "control_request", "control_cancel_request", "stream_event", "keep_alive", "streamlined_text", "streamlined_tool_use_summary", "prompt_suggestion", "future_message", "user", "assistant", "result"}
	subtypes := []string{"session_state_changed", "task_notification", "task_started", "task_progress", "post_turn_summary", "init", "future_system"}
	type row struct {
		Verbose  bool              `json:"verbose"`
		Messages []json.RawMessage `json:"messages"`
	}
	rows := []row{}
	for _, verbose := range []bool{false, true} {
		rows = append(rows, row{verbose, []json.RawMessage{}}, row{verbose, base}, row{verbose, base[:2]})
		for _, kind := range types {
			message, _ := json.Marshal(map[string]any{"type": kind, "result": "late event"})
			rows = append(rows, row{verbose, append(append([]json.RawMessage{}, base...), message)})
		}
		for _, subtype := range subtypes {
			message, _ := json.Marshal(map[string]any{"type": "system", "subtype": subtype})
			rows = append(rows, row{verbose, append(append([]json.RawMessage{}, base...), message)})
		}
		rows = append(rows, row{verbose, []json.RawMessage{json.RawMessage(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["physical failure"]}`)}})
	}
	payload, err := json.Marshal(map[string]any{"rows": rows})
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-public-print-json.fixture.ts")
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		bun = "bun"
	}
	type observation struct {
		Stdout string `json:"stdout,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	runSource := func() []observation {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + home, "CC_HAHA_PUBLIC_PRINT_JSON_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
		cmd.Stdin = bytes.NewReader(payload)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("actual Source formatting: %v stdout%s stderr%s", err, stdout.Bytes(), stderr.Bytes())
		}
		var observed []observation
		if err := json.Unmarshal(stdout.Bytes(), &observed); err != nil {
			t.Fatal(err, stdout.String())
		}
		return observed
	}
	expected := runSource()
	if !reflect.DeepEqual(expected, runSource()) || len(expected) != len(rows) {
		t.Fatal("original formatting replay changed")
	}
	for index, row := range rows {
		t.Run(string(rune('A'+index)), func(t *testing.T) {
			collector := printJSONCollector{verbose: row.Verbose}
			for _, message := range row.Messages {
				collector.observe(message)
			}
			document, err := collector.document()
			observed := observation{}
			if err != nil {
				observed.Error = err.Error()
			} else {
				observed.Stdout = string(document) + "\n"
			}
			if !reflect.DeepEqual(observed, expected[index]) {
				t.Fatalf("original public JSON mismatch: got%+v want%+v", observed, expected[index])
			}
			if !row.Verbose && len(collector.messages) != 0 {
				t.Fatal("nonverbose output accumulated the full SDK array")
			}
		})
	}
}
