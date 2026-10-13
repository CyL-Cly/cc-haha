package queryloop

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceToolDocumentSupplementAndNestedResultsNormalize(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	sources := []string{`{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQ="}`, `{"type":"text","media_type":"text/plain","data":"文档🙂"}`, `{"type":"url","url":"https://private.invalid/file.pdf"}`, `{"type":"content","content":"original inline content"}`, `{"type":"content","content":[{"type":"text","text":"actual nested content"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]}`}
	rows := []protocol.JSONObject{}
	for index, source := range sources {
		for _, nested := range []bool{false, true} {
			block := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"document"`)}, protocol.JSONField{Name: "source", Value: json.RawMessage(source)}, protocol.JSONField{Name: "future", Value: json.RawMessage(`{"keep":true}`)})
			content, _ := json.Marshal([]protocol.JSONObject{block})
			if nested {
				content, _ = json.Marshal([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"tool_result"`)}, protocol.JSONField{Name: "tool_use_id", Value: json.RawMessage(`"actual-document-tool"`)}, protocol.JSONField{Name: "content", Value: content})})
			}
			data, _ := json.Marshal(map[string]any{"type": "user", "uuid": "00000000-0000-4000-8000-000000000001", "timestamp": "2026-10-07T00:00:00Z", "isMeta": true, "message": map[string]any{"role": "user", "content": json.RawMessage(content)}})
			var record protocol.JSONObject
			if err := record.UnmarshalJSON(data); err != nil {
				t.Fatal(index, err)
			}
			rows = append(rows, record)
		}
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-tool-document.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TOOL_DOCUMENT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected [][]protocol.JSONObject
	if err := json.Unmarshal(output, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(output))
	}
	for index, row := range rows {
		actual, err := NormalizeOrdinaryToolResult(Message{Record: row})
		if err != nil {
			t.Fatal(index, err)
		}
		records := []protocol.JSONObject{}
		for _, item := range actual {
			records = append(records, item.Record)
		}
		leftJSON, _ := json.Marshal(records)
		rightJSON, _ := json.Marshal(expected[index])
		var left, right any
		_ = json.Unmarshal(leftJSON, &left)
		_ = json.Unmarshal(rightJSON, &right)
		if !reflect.DeepEqual(left, right) {
			t.Fatal("actual Source document normalized differently", index, string(leftJSON), string(rightJSON))
		}
	}
}
