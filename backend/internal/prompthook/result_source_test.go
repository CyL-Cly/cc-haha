package prompthook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/goalstate"
)

func TestPromptHookResultAndGoalFailureAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		Response      string         `json:"response"`
		Hook          map[string]any `json:"hook"`
		Error         string         `json:"error,omitempty"`
		WaitAbort     bool           `json:"waitAbort,omitempty"`
		ParentAborted bool           `json:"parentAborted,omitempty"`
	}
	rows := []row{}
	for _, prompt := range []string{"ordinary hook $ARGUMENTS", goalstate.CreateHook("finish real work").Prompt} {
		for _, response := range []string{`{"ok":true}`, `{"ok":false,"reason":"missing verification"}`, `{"ok":false}`, `{"ok":true,"reason":"ignored","future":42}`, `not JSON`, `false`, `0e5`, `null`, `[]`, `1`, `{"ok":"yes","reason":null}`, `{"reason":"missing ok"}`, `{"ok":true} garbage`, "\ufeff {\"ok\":true} \n"} {
			rows = append(rows, row{Response: response, Hook: map[string]any{"type": "prompt", "prompt": prompt, "model": "fixture-model", "timeout": 1}})
		}
		rows = append(rows, row{Hook: map[string]any{"type": "prompt", "prompt": prompt, "model": "fixture-model", "timeout": 1}, Error: "physical API failure"}, row{Hook: map[string]any{"type": "prompt", "prompt": prompt, "model": "fixture-model", "timeout": 0.01}, WaitAbort: true}, row{Hook: map[string]any{"type": "prompt", "prompt": prompt, "model": "fixture-model", "timeout": 1}, WaitAbort: true, ParentAborted: true})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/prompt-hook.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PROMPT_HOOK_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_DEFAULT_HAIKU_MODEL=fixture-model"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var source struct {
		Rows []struct {
			Result   map[string]any
			Captured map[string]any
			Length   float64
		}
	}
	if err = json.Unmarshal(data, &source); err != nil {
		t.Fatal(err, string(data))
	}
	for i, row := range rows {
		var failure error
		if row.Error != "" {
			failure = errors.New(row.Error)
		}
		if row.WaitAbort {
			failure = errors.New("aborted")
		}
		result := Interpret(row.Hook["prompt"].(string), "Stop", "Stop", "actual-tool-id", row.Response, failure, row.ParentAborted, row.WaitAbort && !row.ParentAborted)
		actual := map[string]any{"hook": row.Hook, "outcome": result.Outcome}
		if result.Payload != nil {
			actual["message"] = map[string]any{"type": "attachment", "attachment": result.Payload}
		}
		if result.BlockingError != "" {
			actual["blockingError"] = map[string]any{"blockingError": result.BlockingError, "command": result.Command}
		}
		if result.PreventContinuation {
			actual["preventContinuation"] = true
		}
		if result.StopReason != nil {
			var reason any
			_ = json.Unmarshal(result.StopReason, &reason)
			actual["stopReason"] = reason
		}
		raw, _ := json.Marshal(actual)
		var decoded map[string]any
		_ = json.Unmarshal(raw, &decoded)
		if !reflect.DeepEqual(decoded, source.Rows[i].Result) {
			t.Errorf("row%d Go%s Source%v", i, raw, source.Rows[i].Result)
		}
	}
}
