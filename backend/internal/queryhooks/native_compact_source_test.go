package queryhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCompactHookWrappersMatchActualFrozenSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	rows := []map[string]any{}
	for _, event := range []string{"PreCompact", "PostCompact"} {
		for _, trigger := range []string{"manual", "auto"} {
			var settings protocol.JSONObject
			_ = settings.UnmarshalJSON([]byte(`{"hooks":{"` + event + `":[{"matcher":"manual|auto","hooks":[{"type":"command","command":"success"},{"type":"command","command":"empty"},{"type":"command","command":"failure"}]}]}}`))
			rows = append(rows, map[string]any{"event": event, "sid": "fixture-session", "wrapper": true, "fields": map[string]any{"trigger": trigger, "custom_instructions": "user directives", "compact_summary": "model summary"}, "settings": settings, "outputs": map[string]CommandOutput{"success": {Stdout: "\ufeff remember constraint \ufeff"}, "empty": {}, "failure": {Code: 1, Stderr: " failed hook "}}, "http": map[string]any{}})
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/tmux-worktree-hook.fixture.ts")
	sourceRows := []map[string]any{}
	for _, row := range rows {
		copy := map[string]any{}
		for k, v := range row {
			copy[k] = v
		}
		outputs := map[string]any{}
		for name, v := range row["outputs"].(map[string]CommandOutput) {
			outputs[name] = map[string]any{"stdout": v.Stdout, "stderr": v.Stderr, "code": v.Code}
		}
		copy["outputs"] = outputs
		sourceRows = append(sourceRows, copy)
	}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": sourceRows, "timeouts": []string{}})
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OUTSIDE_HOOK_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var source struct{ Rows []map[string]string }
	if e = json.Unmarshal(output, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatal(e, string(output))
	}
	for i, row := range rows {
		owner := cooperative.New()
		settings := row["settings"].(protocol.JSONObject)
		service := &Service{options: Options{MainSessionID: "fixture-session", UUID: func() string { return "hook-id" }, MergedSettings: func() (*protocol.JSONObject, error) { return &settings, nil }}, env: map[string]string{}, registry: registryState{}}
		service.options.Commands = &CommandBindings{Owner: owner, Store: &CommandStore{events: map[string]map[string][]commandMatcher{}}, Input: func(_ context.Context, c Context) (protocol.JSONObject, error) {
			fields := c.InputFields.Clone()
			fields.Set("hook_event_name", commandJSON(c.HookEvent))
			return fields, nil
		}, Run: func(_ context.Context, c Context, hook, input protocol.JSONObject) (CommandOutput, error) {
			fields := row["fields"].(map[string]any)
			if input.String("trigger") != fields["trigger"] {
				t.Error("wrapper lost trigger")
			}
			return row["outputs"].(map[string]CommandOutput)[hook.String("command")], nil
		}}
		fields := row["fields"].(map[string]any)
		var got CompactHookResult
		e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			var err error
			got, err = service.ExecuteCompactHooks(context.Background(), row["event"].(string), fields["trigger"].(string), "user directives", "model summary", Context{Scope: scope})
			return err
		})
		owner.Close()
		if e != nil {
			t.Fatal(e)
		}
		mapped := map[string]string{}
		if got.Instructions != "" {
			mapped["newCustomInstructions"] = got.Instructions
		}
		if got.Display != "" {
			mapped["userDisplayMessage"] = got.Display
		}
		if !reflect.DeepEqual(mapped, source.Rows[i]) {
			t.Error(i, mapped, source.Rows[i])
		}
	}
}
