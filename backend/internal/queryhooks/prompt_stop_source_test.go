package queryhooks

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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/goalstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestNativePromptStopAggregationMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []protocol.JSONObject{}
	for _, prompt := range []string{"ordinary prompt", goalstate.CreateHook("finish actual work").Prompt} {
		for _, response := range []string{`{"ok":true}`, `{"ok":false,"reason":"finish <then> & verify"}`, `broken JSON`, `{"ok":false,"reason":"lone\ud800 and pair😀"}`} {
			for _, status := range []string{"", "custom display"} {
				hook := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("prompt")}, protocol.JSONField{Name: "prompt", Value: commandJSON(prompt)}, protocol.JSONField{Name: "model", Value: commandJSON("fixture-model")}, protocol.JSONField{Name: "timeout", Value: commandJSON(1)})
				if status != "" {
					hook.Set("statusMessage", commandJSON(status))
				}
				rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "hook", Value: commandJSON(hook)}, protocol.JSONField{Name: "response", Value: commandJSON(response)}))
			}
		}
	}
	for _, prompt := range []string{"ordinary prompt", goalstate.CreateHook("finish actual work").Prompt} {
		hook := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("prompt")}, protocol.JSONField{Name: "prompt", Value: commandJSON(prompt)}, protocol.JSONField{Name: "model", Value: commandJSON("fixture-model")}, protocol.JSONField{Name: "timeout", Value: commandJSON(0.01)})
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "hook", Value: commandJSON(hook)}, protocol.JSONField{Name: "waitAbort", Value: commandJSON(true)}))
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/prompt-stop.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PROMPT_STOP_FIXTURE=1", "PATH=/usr/bin:/bin", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"home": home, "rows": rows}))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var source struct{ Rows []json.RawMessage }
	if err = json.Unmarshal(data, &source); err != nil {
		t.Fatal(err, string(data))
	}
	for i, row := range rows {
		owner := cooperative.New()
		factory, _ := messages.NewFactory(func() string { return "fixture" }, func() time.Time { return time.Unix(0, 0) })
		hook, _ := row.Object("hook")
		s := &Service{options: Options{MainSessionID: "goal-source-session", UUID: func() string { return "fixture" }, Commands: &CommandBindings{Owner: owner, Factory: factory, Timestamp: func() string { return "fixture" }, NowMillis: func() float64 { return 0 }, Store: &CommandStore{events: map[string]map[string][]commandMatcher{}}, Input: func(context.Context, Context) (protocol.JSONObject, error) {
			return protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: commandJSON("goal-source-session")}), nil
		}}, Prompts: &PromptBindings{Owner: owner, Query: func(ctx context.Context, _ *cooperative.Scope, _ PromptModelInput) (protocol.JSONObject, error) {
			if string(row.Get("waitAbort")) == "true" {
				<-ctx.Done()
				return protocol.JSONObject{}, ctx.Err()
			}
			if row.String("error") != "" {
				return protocol.JSONObject{}, errors.New(row.String("error"))
			}
			return protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: commandJSON([]map[string]string{{"type": "text", "text": row.String("response")}})}), nil
		}}}}
		observed := []protocol.JSONObject{}
		var decision queryloop.StopDecision
		err = owner.Run(ctx, func(scope *cooperative.Scope) error {
			var err error
			decision, err = s.executeNativeCommandStop(ctx, Context{Scope: scope}, "Stop", "prompt-stop-id", []commandMatcher{{hook: hook}}, func(value queryloop.Yield) error {
				if value.Message != nil {
					record := value.Message.Record.Clone()
					record.Delete("uuid")
					record.Delete("timestamp")
					observed = append(observed, record)
				}
				return nil
			})
			return err
		})
		owner.Close()
		if err != nil {
			t.Fatal(i, err)
		}
		blockers := []protocol.JSONObject{}
		for _, m := range decision.BlockingMessages {
			record := m.Record.Clone()
			record.Delete("uuid")
			record.Delete("timestamp")
			blockers = append(blockers, record)
		}
		var actual map[string]any
		for _, record := range observed {
			if record.String("type") == "attachment" {
				att, _ := record.Object("attachment")
				if att.Get("durationMs") != nil {
					att.Set("durationMs", commandJSON(0))
					record.Set("attachment", commandJSON(att))
				}
			}
			if record.String("subtype") == "stop_hook_summary" {
				var infos []protocol.JSONObject
				_ = json.Unmarshal(record.Get("hookInfos"), &infos)
				for _, info := range infos {
					if info.Get("durationMs") != nil {
						info.Set("durationMs", commandJSON(0))
					}
				}
				record.Set("hookInfos", commandJSON(infos))
			}
		}
		_ = json.Unmarshal(commandJSON(map[string]any{"messages": observed, "result": map[string]any{"blockingErrors": blockers, "preventContinuation": decision.PreventContinuation}}), &actual)
		var expected map[string]any
		_ = json.Unmarshal(source.Rows[i], &expected)
		actualRaw := commandJSON(map[string]any{"messages": observed, "result": map[string]any{"blockingErrors": blockers, "preventContinuation": decision.PreventContinuation}})
		if bytes.Count(actualRaw, []byte(`\ud800`)) != bytes.Count(source.Rows[i], []byte(`\ud800`)) {
			t.Errorf("UTF16 source count%d Go%s Source%s", i, actualRaw, source.Rows[i])
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("case%d Go%s Source%s", i, commandJSON(actual), commandJSON(source.Rows[i]))
		}
	}
}
