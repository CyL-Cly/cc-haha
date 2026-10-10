package sidequestion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceSideQuestionContextPendingGroupsAndWrappedPrompt(t *testing.T) {
	user := `{"type":"user","uuid":"old-user","message":{"role":"user","content":"old"}}`
	tool := `{"type":"assistant","uuid":"old-tool","message":{"id":"group","stop_reason":"tool_use","content":[{"type":"tool_use","id":"one","name":"Read","input":{}}]}}`
	result := `{"type":"user","uuid":"old-result","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"one","content":"done"}]}}`
	partial := `{"type":"assistant","uuid":"partial","message":{"id":"partial-group","stop_reason":null,"content":[{"type":"text","text":"incomplete"}]}}`
	partialThinking := `{"type":"assistant","uuid":"partial-thinking","message":{"id":"partial-group","content":[{"type":"thinking","thinking":"unfinished"}]}}`
	boundary := `{"type":"system","subtype":"compact_boundary","uuid":"boundary"}`
	inputs := []json.RawMessage{json.RawMessage(`{"questionId":"11111111-1111-4111-8111-111111111111","question":"  current \ud800  ","history":[{"question":" prior ","response":"old \udfff"}]}`)}
	histories := [][]json.RawMessage{
		{json.RawMessage(user)},
		{json.RawMessage(user), json.RawMessage(tool)},
		{json.RawMessage(user), json.RawMessage(tool), json.RawMessage(result)},
		{json.RawMessage(user), json.RawMessage(partial), json.RawMessage(partialThinking)},
		{json.RawMessage(tool), json.RawMessage(boundary), json.RawMessage(user)},
		{json.RawMessage(tool), json.RawMessage(result), json.RawMessage(tool)},
	}
	contexts := []any{}
	actual := []any{}
	for _, history := range histories {
		contexts = append(contexts, map[string]any{"history": history, "input": inputs[0]})
		rows := []protocol.JSONObject{}
		for _, data := range history {
			rows = append(rows, object(data))
		}
		before, _ := json.Marshal(rows)
		allocated := 0
		factory, _ := messages.NewFactory(func() string { allocated++; return fmt.Sprintf("allocated-%d", allocated) }, func() time.Time { return time.UnixMilli(1700000000000) })
		prepared, e := Prepare(rows, factory)
		if e != nil {
			t.Fatal(e)
		}
		allocated = 0
		input, e := Parse(inputs[0])
		if e != nil {
			t.Fatal(e)
		}
		prompt, e := PromptMessages(input, factory)
		if e != nil {
			t.Fatal(e)
		}
		after, _ := json.Marshal(rows)
		actual = append(actual, map[string]any{"prepared": prepared, "prompt": prompt, "skipTranscript": true, "skipCacheWrite": true, "maxTurns": 1, "parentUnchanged": bytes.Equal(before, after)})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../testdata/side-question-api.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SIDE_QUESTION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	data, _ := json.Marshal(map[string]any{"contexts": contexts})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source", e, stderr.String())
	}
	var expected, normalized []any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e, string(out))
	}
	data, _ = json.Marshal(actual)
	_ = json.Unmarshal(data, &normalized)
	if !reflect.DeepEqual(expected, normalized) {
		t.Fatalf("actual Source=%s Go=%s", out, data)
	}
}
