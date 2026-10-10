package goalstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestGoalStateParserHookIdentityReplacementCleanupAndTranscriptAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	args := []string{"", " ", "\ufeffclear\u00a0", "clear", "Clear", "status", "pause", "resume", "complete", "--tokens 100 goal", "--tokensMore", "literal task\nthen verify", "\u0085visible", "\u3000中文😀\u3000"}
	contents := []string{Marker + "\n<goal-objective> objective\nlines </goal-objective>", "<goal-objective>wrong marker</goal-objective>", "<LOCAL-COMMAND-STDOUT>Goal set: x</LOCAL-COMMAND-STDOUT>", "<local-command-stderr>Goal continuing: x</local-command-stderr>", "<local-command-stdout></local-command-stdout><local-command-stderr>Goal set: ignored</local-command-stderr>", "<local-command-stdout>No active goal.</local-command-stdout>"}
	continuations := []string{"", "Prompt hook condition was not met:  <then> & verify\nnext", "PROMPT HOOK CONDITION WAS NOT MET:\ufeff  finish", strings.Repeat("a", 239) + "😀rest", "\u0085keep\u0085", "\u3000中文\u2028验证"}
	message := func(text string) map[string]any { return map[string]any{"type": "system", "content": text} }
	transcript := []any{message("<command-name>/goal</command-name>"), message("<local-command-stdout>Goal set: restore target</local-command-stdout>")}
	actions := []map[string]any{{"type": "set", "sid": "one", "objective": " first objective ", "now": 1000}, {"type": "set", "sid": "one", "objective": "second objective", "now": 2000}, {"type": "success", "sid": "one", "index": 0}, {"type": "clear", "sid": "one"}, {"type": "restore", "sid": "one", "messages": transcript, "now": 3000}, {"type": "set", "sid": "one", "objective": "new objective", "now": 4000}, {"type": "success", "sid": "one", "index": 2}, {"type": "restore", "sid": "two", "messages": transcript, "now": 5000}, {"type": "restore", "sid": "two", "messages": []any{}, "now": 6000}, {"type": "clear", "sid": "two"}, {"type": "clear", "sid": "missing"}, {"type": "set", "sid": "equal", "objective": "same", "now": 7000}, {"type": "set", "sid": "equal", "objective": "same", "now": 8000}, {"type": "success", "sid": "equal", "index": 4}, {"type": "restore", "sid": "done", "messages": append(append([]any{}, transcript...), message("<local-command-stdout>Goal marked complete.</local-command-stdout>")), "now": 9000}}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/goal-state.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GOAL_STATE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	data, _ := json.Marshal(map[string]any{"home": home, "arguments": args, "contents": contents, "actions": actions, "continuations": continuations})
	cmd.Stdin = bytes.NewReader(data)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(raw))
	}
	var source struct {
		Parsed, Outputs, Rows []map[string]any
		Continuations         []json.RawMessage
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err, string(raw))
	}
	for i, reason := range continuations {
		a, _ := protocol.DecodeJavaScriptJSONString(ContinuationOutputJSON(reason))
		b, _ := protocol.DecodeJavaScriptJSONString(source.Continuations[i])
		if !reflect.DeepEqual(a, b) {
			t.Errorf("continuation%d Go%s Source%s", i, ContinuationOutputJSON(reason), source.Continuations[i])
		}
	}
	for i, input := range args {
		result, err := Parse(input)
		actual := map[string]any{}
		if err != nil {
			actual["error"] = err.Error()
		} else {
			actual["type"] = result.Type
			if result.Type == "set" {
				actual["objective"] = result.Objective
			}
		}
		if !reflect.DeepEqual(actual, source.Parsed[i]) {
			t.Errorf("args%q Go%v Source%v", input, actual, source.Parsed[i])
		}
	}
	for i, input := range contents {
		var objective any
		if value := ObjectiveFromPrompt(input); value != "" {
			objective = value
		}
		actual := map[string]any{"prompt": IsGoalPrompt(input), "objective": objective, "local": IsLocalOutput(input)}
		if !reflect.DeepEqual(actual, source.Outputs[i]) {
			t.Errorf("content%q Go%v Source%v", input, actual, source.Outputs[i])
		}
	}
	owner := cooperative.New()
	defer owner.Close()
	hooks := map[string][]*Hook{}
	callbacks := []func(*cooperative.Scope) error{}
	state, err := New(Options{Owner: owner, Add: func(scope *cooperative.Scope, sid string, hook *Hook, done func(*cooperative.Scope) error) error {
		if !scope.BelongsTo(owner) {
			return cooperative.ErrRestricted
		}
		hooks[sid] = append(hooks[sid], hook)
		callbacks = append(callbacks, done)
		return nil
	}, Remove: func(scope *cooperative.Scope, sid string, hook *Hook) error {
		if !scope.BelongsTo(owner) {
			return cooperative.ErrRestricted
		}
		next := []*Hook{}
		for _, candidate := range hooks[sid] {
			if candidate.Prompt != hook.Prompt || candidate.Timeout != hook.Timeout {
				next = append(next, candidate)
			}
		}
		hooks[sid] = next
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	project := func(goal *Goal) any {
		if goal == nil {
			return nil
		}
		return map[string]any{"threadId": goal.ThreadID, "objective": goal.Objective, "createdAt": goal.CreatedAt, "hook": map[string]any{"type": "prompt", "prompt": goal.Hook.Prompt, "timeout": goal.Hook.Timeout}}
	}
	if err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		for i, action := range actions {
			sid := action["sid"].(string)
			var result *Goal
			var err error
			switch action["type"] {
			case "set":
				result, err = state.Set(scope, sid, action["objective"].(string), float64(action["now"].(int)))
			case "clear":
				result, err = state.Clear(scope, sid)
			case "success":
				err = callbacks[action["index"].(int)](scope)
			case "restore":
				raw, _ := json.Marshal(action["messages"])
				var messages []protocol.JSONObject
				_ = json.Unmarshal(raw, &messages)
				result, err = state.Restore(scope, sid, messages, float64(action["now"].(int)))
			}
			if err != nil {
				return err
			}
			current, err := state.Get(scope, sid)
			if err != nil {
				return err
			}
			hookRows := []any{}
			for _, hook := range hooks[sid] {
				hookRows = append(hookRows, map[string]any{"type": "prompt", "prompt": hook.Prompt, "timeout": hook.Timeout})
			}
			actual := map[string]any{"result": project(result), "current": project(current), "hooks": hookRows}
			actualJSON, _ := json.Marshal(actual)
			expectedJSON, _ := json.Marshal(source.Rows[i])
			if !bytes.Equal(actualJSON, expectedJSON) {
				return fmt.Errorf("row%d Go%s Source%s", i, actualJSON, expectedJSON)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := state.Get(scope, "one"); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign Get accepted: %v", err)
		}
		if _, err := state.Set(scope, "one", "foreign", 1); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign Set accepted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
