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
	"time"
)

func TestActualSourceSDKCallbackOutsideTimeoutCoercionAndOutputFlags(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{}
	for _, timeout := range []json.RawMessage{[]byte(`-1`), []byte(`0.001`), []byte(`"bad"`), []byte(`1e400`), []byte(`2147484`), []byte(`[]`), []byte(`true`)} {
		rows = append(rows, map[string]any{"name": string(timeout), "id": "timeout629", "event": "WorktreeCreate", "hold": true, "timeout": timeout})
	}
	rows = append(rows, map[string]any{"name": "async", "id": "async629", "event": "WorktreeCreate", "response": map[string]any{"async": true}}, map[string]any{"name": "block", "id": "block629", "event": "WorktreeRemove", "response": map[string]any{"decision": "block", "systemMessage": "note629"}}, map[string]any{"name": "empty-path", "id": "empty629", "event": "WorktreeCreate", "response": map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "WorktreeCreate", "worktreePath": ""}, "systemMessage": "ignore629"}})
	fixture, _ := filepath.Abs("../../testdata/native-sdk-worktree-callback.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_WORKTREE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(commandJSON(map[string]any{"sid": "sid629", "path": filepath.Join(home, "tree"), "rows": rows}))
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	source, e := command.Output()
	if e != nil {
		t.Fatal("actual timer/Source callback consumer", e, diagnostics.String())
	}
	var expected struct {
		Rows []struct {
			Name   string
			Result []OutsideResult
			Cancel protocol.JSONObject
		}
	}
	if e = json.Unmarshal(source, &expected); e != nil || len(expected.Rows) != len(rows) {
		t.Fatal(e, string(source))
	}
	for i, row := range rows {
		t.Run(row["name"].(string), func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			settings := protocol.NewJSONObject()
			s := &Service{env: map[string]string{}, options: Options{Profile: ResolvedSettingsProfile, UUID: func() string { return "hook629" }, MainSessionID: "sid629", MergedSettings: func() (*protocol.JSONObject, error) { return &settings, nil }}}
			s.options.Commands = &CommandBindings{Owner: owner, Input: func(_ context.Context, call Context) (protocol.JSONObject, error) {
				return protocol.NewJSONObject(), nil
			}}
			descriptor := protocol.NewJSONObject(protocol.JSONField{Name: "hooks", Value: commandJSON([]any{map[string]any{"type": "callback", "callback_id": row["id"], "timeout": row["timeout"]}})})
			store := &CallbackStore{events: map[string][]protocol.JSONObject{row["event"].(string): []protocol.JSONObject{descriptor}}}
			s.options.Callbacks = &CallbackBindings{Owner: owner, Store: store, Run: func(ctx context.Context, _ Context, _, _ protocol.JSONObject) (protocol.JSONObject, error) {
				if row["hold"] == true {
					select {
					case <-ctx.Done():
						return protocol.NewJSONObject(), nil
					case <-time.After(3 * time.Second):
						t.Error("actual native callback timer failed to cancel")
						return protocol.NewJSONObject(), nil
					}
				}
				var result protocol.JSONObject
				err := result.UnmarshalJSON(commandJSON(row["response"]))
				return result, err
			}}
			var got []OutsideResult
			e = owner.Run(ctx, func(scope *cooperative.Scope) error {
				var e error
				got, e = s.ExecuteOutside(ctx, OutsideInput{Event: row["event"].(string), SessionID: "sid629", Timeout: time.Second, Call: Context{Scope: scope}})
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(got, expected.Rows[i].Result) {
				t.Fatal("original callback output flags", got, expected.Rows[i].Result)
			}
			if row["hold"] == true && expected.Rows[i].Cancel.String("type") != "control_cancel_request" {
				t.Fatal("Source timer did not cancel real SDK callback")
			}
		})
	}
	// Falsy numeric representations select the original outer timeout, whereas
	// truthy string '0' is multiplied and becomes a minimum timer tick.
	for _, raw := range []string{"0", "-0", "0.0", "0e99", "null", "false", "\"\""} {
		got, e := sdkCallbackTimeout([]byte(raw), time.Minute)
		if e != nil || got != time.Minute {
			t.Fatal("Source numeric-falsy fallback", raw, got, e)
		}
	}
}
