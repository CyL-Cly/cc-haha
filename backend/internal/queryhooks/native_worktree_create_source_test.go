package queryhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceTmuxWorktreeCreatePresenceFirstSuccessFailureAndWhitespace(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	rows := []map[string]any{}
	for _, output := range []map[string]CommandOutput{
		{"first": {Stdout: "  /actual/first\n"}, "second": {Stdout: "/actual/second"}},
		{"first": {Code: 2, Stderr: "source failed"}, "second": {Stdout: "\ufeff/actual/second\ufeff"}},
		{"first": {Stdout: " \n"}, "second": {Stdout: "\ufeff \n"}},
		{"first": {Code: 2, Stderr: "\ufeffbad one\ufeff"}, "second": {Code: 1, Stderr: ""}},
		{"first": {Aborted: true}, "second": {Stdout: "/actual/after-cancelled"}},
	} {
		raw := `{"hooks":{"WorktreeCreate":[{"hooks":[{"type":"command","command":"first"},{"type":"command","command":"second"}]}]}}`
		var settings protocol.JSONObject
		_ = settings.UnmarshalJSON([]byte(raw))
		outs := map[string]any{}
		for name, v := range output {
			outs[name] = map[string]any{"stdout": v.Stdout, "stderr": v.Stderr, "code": v.Code, "aborted": v.Aborted}
		}
		rows = append(rows, map[string]any{"event": "WorktreeCreate", "sid": "actual-sid", "name": "actual/name", "settings": settings, "outputs": outs, "http": map[string]any{}})
	}
	for _, raw := range []string{`{"hooks":{"WorktreeCreate":[{"hooks":[]}]}}`, `{"hooks":{"WorktreeCreate":[]}}`, `{}`} {
		var settings protocol.JSONObject
		_ = settings.UnmarshalJSON([]byte(raw))
		rows = append(rows, map[string]any{"event": "WorktreeCreate", "sid": "actual-sid", "name": "actual/name", "settings": settings, "outputs": map[string]any{}, "http": map[string]any{}})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/tmux-worktree-hook.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows, "timeouts": []string{}})
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "TMPDIR=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OUTSIDE_HOOK_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	raw, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(raw))
	}
	var source struct{ Rows []map[string]any }
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e, string(raw))
	}
	for i, row := range rows {
		owner := cooperative.New()
		settings := row["settings"].(protocol.JSONObject)
		service := &Service{options: Options{UUID: func() string { return "owned-hook" }, MergedSettings: func() (*protocol.JSONObject, error) { return &settings, nil }}, env: map[string]string{}, registry: registryState{}}
		service.options.Commands = &CommandBindings{Owner: owner, Store: &CommandStore{events: map[string]map[string][]commandMatcher{}}, Input: func(_ context.Context, c Context) (protocol.JSONObject, error) {
			return protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: commandJSON("actual-sid")}, protocol.JSONField{Name: "transcript_path", Value: commandJSON("/original/transcript")}, protocol.JSONField{Name: "cwd", Value: commandJSON("/original/cwd")}, protocol.JSONField{Name: "hook_event_name", Value: commandJSON(c.HookEvent)}, protocol.JSONField{Name: "name", Value: c.InputFields.Get("name")}), nil
		}, Run: func(_ context.Context, c Context, h, input protocol.JSONObject) (CommandOutput, error) {
			if c.Scope == nil || !c.Scope.BelongsTo(owner) {
				return CommandOutput{}, cooperative.ErrRestricted
			}
			if input.String("name") != "actual/name" || input.String("hook_event_name") != "WorktreeCreate" {
				t.Error("actual wrapper input changed", input)
			}
			out := row["outputs"].(map[string]any)[h.String("command")].(map[string]any)
			return CommandOutput{Stdout: out["stdout"].(string), Stderr: out["stderr"].(string), Code: out["code"].(int), Aborted: out["aborted"].(bool)}, nil
		}}
		has, e := service.HasWorktreeCreateHooks("actual-sid")
		if e != nil {
			t.Fatal(e)
		}
		actual := map[string]any{"has": has}
		e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			path, e := service.ExecuteWorktreeCreateHook(context.Background(), "actual/name", "actual-sid", Context{Scope: scope})
			if e != nil {
				actual["error"] = e.Error()
			} else {
				actual["path"] = path
			}
			return nil
		})
		owner.Close()
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(source.Rows[i], actual) {
			t.Fatalf("actual wrapper %d source=%#v go=%#v", i, source.Rows[i], actual)
		}
	}
}
