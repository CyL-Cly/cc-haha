package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceSDKPromptSuggestionFiltersAndFirstAssistantText(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source Bun required")
	}
	fixture := os.Getenv("CC_HAHA_PROMPT_SUGGESTION_SOURCE")
	if fixture == "" {
		fixture, _ = filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-prompt-suggestions.fixture.ts")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{"", "done", "nothing found", "nothing found.", "no suggestion today", "nothing to suggest here", "silence is best", "stays silent", "...silence...", "(let them assess)", "[no suggestion]", "API Error: private", "prompt is too long now", "request timed out", "invalid api key", "image was too large", "Next:\u00a0run tests", "Next: run tests", "foo", "yes", "/test", "run the tests", "one two three four five six seven eight nine ten eleven twelve thirteen", strings.Repeat("\U0001f600", 48) + " run", "Run tests. Then commit", "run\u00a0the tests", "run\ntests", "run *tests*", "looks good now", "thanks for that", "I'll run tests", "continue now", "(line\rbreak)", "(line\u2028break)", "NİCE things", "i thinK"}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_PROMPT_SUGGESTION_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "ANTHROPIC_API_KEY=fake-suggestion", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CLAUDE_CODE_MAX_RETRIES=0", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"suggestions": []any{}, "filters": texts}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source filters", err, stderr.String())
	}
	var source struct {
		Filters []struct {
			Text     string
			Filtered bool
		}
		Capabilities protocol.JSONObject
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Filters) != len(texts) {
		t.Fatal(err, string(data))
	}
	if string(source.Capabilities.Get("blocked")) != "[]" {
		t.Fatal("Source capability escape", string(raw(source.Capabilities)))
	}
	for i, row := range source.Filters {
		if row.Text != texts[i] || sdkSuggestionFiltered(row.Text) != row.Filtered {
			t.Error("actual Source filter differs", i, row.Text, row.Filtered)
		}
	}
	row := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("assistant")}, protocol.JSONField{Name: "message", Value: raw(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}})})
	}
	empty := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("assistant")}, protocol.JSONField{Name: "message", Value: raw(map[string]any{"content": []any{map[string]any{"type": "text", "text": ""}, map[string]any{"type": "text", "text": "WRONG_SECOND_BLOCK"}}})})
	if value := sdkSuggestionText([]protocol.JSONObject{empty, row("\u00a0run the tests\u00a0"), row("WRONG_LATER_ASSISTANT")}); string(value) != `"run the tests"` {
		t.Fatal("first text block / all assistant selection", value)
	}
	var surrogate protocol.JSONObject
	if err := surrogate.UnmarshalJSON([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"\ud800 go"}]}}`)); err != nil {
		t.Fatal(err)
	}
	if text := sdkSuggestionText([]protocol.JSONObject{surrogate}); string(text) != `"\ud800 go"` || sdkSuggestionFilteredJSON(text) {
		t.Fatal("suggestion lost original UTF-16 code units", string(text))
	}
}

// The Source oracle's held-result row above runs the real model/fork and
// original release body. This adjacent consumer test binds the same boundary
// to the actual native held-result/task store, including a new-input discard.
func TestNativeSDKPendingSuggestionWaitsForHeldResultAndNewInputDiscards(t *testing.T) {
	for _, discard := range []bool{false, true} {
		owner := cooperative.New()
		var output []protocol.JSONObject
		running := true
		h := &Host{options: Options{Owner: owner, Print: &PrintOptions{NativeProcess: true}, OnPhase: func(string) {}, RunState: &RunStateOptions{GetTasks: func(scope *cooperative.Scope) ([]BackgroundTask, error) {
			if !scope.BelongsTo(owner) {
				return nil, cooperative.ErrRestricted
			}
			return []BackgroundTask{{Type: "local_agent", Running: running, Background: true}}, nil
		}}, Output: Output{Enqueue: func(data []byte) error {
			var value protocol.JSONObject
			if err := value.UnmarshalJSON(data); err != nil {
				return err
			}
			output = append(output, value)
			return nil
		}}}, sdkSuggestions: &nativeSDKSuggestions{}}
		err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			result := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("result")})
			held, err := h.holdNativeBackgroundResult(scope, result)
			if err != nil {
				return err
			}
			if !held {
				return fmt.Errorf("running actual background task did not hold result")
			}
			pending := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("prompt_suggestion")}, protocol.JSONField{Name: "suggestion", Value: quote("run the tests")})
			h.sdkSuggestions.pending = &pending
			if err = h.FlushHeldBackgroundResult(scope); err != nil {
				return err
			}
			if len(output) != 0 {
				return fmt.Errorf("result/suggestion escaped running background task")
			}
			if discard {
				h.cancelSDKPromptSuggestion()
			}
			running = false
			return h.FlushHeldBackgroundResult(scope)
		})
		owner.Close()
		if err != nil {
			t.Fatal(err)
		}
		want := 2
		if discard {
			want = 1
		}
		if len(output) != want || output[0].String("type") != "result" || !discard && output[1].String("type") != "prompt_suggestion" {
			t.Fatal("Source held release/discard output order", discard, string(raw(output)))
		}
	}
}
