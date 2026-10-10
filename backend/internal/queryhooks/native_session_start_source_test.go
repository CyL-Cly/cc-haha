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
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSessionStartWrapperProcessMessagesInitialPromptWatchPathsAndMatchingSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		Source           string                   `json:"source"`
		Base             protocol.JSONObject      `json:"base"`
		Settings         protocol.JSONObject      `json:"settings"`
		Outputs          map[string]CommandOutput `json:"outputs"`
		Simple           bool                     `json:"simple"`
		AgentType, Model json.RawMessage
	}
	rows := []row{}
	for _, source := range []string{"startup", "resume", "clear", "compact"} {
		for _, body := range []string{"plain original context", `{"decision":"block","reason":"ignored startup block","continue":false}`, `{"systemMessage":"note\ud800","hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"extra\ud800","initialUserMessage":"initial\ud800","watchPaths":["/original/watch","/original/second"]}}`, `{"continue":"invalid"}`} {
			var settings protocol.JSONObject
			_ = settings.UnmarshalJSON([]byte(`{"future":true,"hooks":{"SessionStart":[{"matcher":"startup|resume|clear|compact","hooks":[{"type":"command","command":"original-one","statusMessage":"old"},{"type":"http","url":"http://127.0.0.1:1/must-not-run"},{"type":"command","command":"original-one","statusMessage":"last"},{"type":"command","command":"skip-if","if":"Read(*)"}]},{"matcher":"never","hooks":[{"type":"command","command":"skip-matcher"}]}]}}`))
			rows = append(rows, row{Source: source, Base: protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: commandJSON("actual-session")}, protocol.JSONField{Name: "transcript_path", Value: commandJSON("/original/transcript")}, protocol.JSONField{Name: "cwd", Value: commandJSON("/original/cwd")}), Settings: settings, Outputs: map[string]CommandOutput{"original-one": {Stdout: body, Output: body}}})
		}
	}
	for _, mode := range []string{"simple", "disabled", "meta"} {
		r := rows[0]
		r.Settings = r.Settings.Clone()
		if mode == "simple" {
			r.Simple = true
		}
		if mode == "disabled" {
			r.Settings.Set("disableAllHooks", []byte("true"))
		}
		if mode == "meta" {
			r.AgentType = commandJSON("main-worker")
			r.Model = commandJSON("startup-model")
		}
		rows = append(rows, r)
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-start.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SESSION_START_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production"}
	// Source transport DTO uses the original lowercase fields; no prepared
	// Hook decisions or retained messages enter either business implementation.
	inputRows := []map[string]any{}
	for _, r := range rows {
		outputs := map[string]any{}
		for name, value := range r.Outputs {
			outputs[name] = map[string]any{"stdout": value.Stdout, "stderr": value.Stderr, "code": value.Code}
		}
		input := map[string]any{"source": r.Source, "base": r.Base, "settings": r.Settings, "outputs": outputs, "simple": r.Simple}
		if r.AgentType != nil {
			input["agentType"] = r.AgentType
		}
		if r.Model != nil {
			input["model"] = r.Model
		}
		inputRows = append(inputRows, input)
	}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"homeDir": home, "rows": inputRows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Rows []struct {
			Input              protocol.JSONObject
			Messages           []protocol.JSONObject
			InitialUserMessage json.RawMessage
			WatchPaths         []json.RawMessage
			Allocations        int
		}
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(output))
	}
	for i, r := range rows {
		owner := cooperative.New()
		factory, _ := messages.NewFactory(func() string { return "fixture" }, func() time.Time { return time.Unix(0, 0) })
		var actualInput protocol.JSONObject
		s := &Service{options: Options{UUID: func() string { return "fixture" }, MergedSettings: func() (*protocol.JSONObject, error) { return &r.Settings, nil }, Commands: &CommandBindings{Owner: owner, Factory: factory, Timestamp: func() string { return "fixture" }, NowMillis: func() float64 { return 0 }, Input: func(_ context.Context, call Context) (protocol.JSONObject, error) {
			value := r.Base.Clone()
			value.Set("permission_mode", nil)
			value.Set("agent_id", nil)
			value.Set("agent_type", nil)
			value.Set("hook_event_name", commandJSON(call.HookEvent))
			for _, field := range call.InputFields.OwnProperties() {
				_ = value.SetPropertyJSON(field.NameJSON, field.Value)
			}
			actualInput = value.Clone()
			return value, nil
		}, Run: func(_ context.Context, _ Context, hook, _ protocol.JSONObject) (CommandOutput, error) {
			return r.Outputs[hook.String("command")], nil
		}}}, registry: registryState{}, env: map[string]string{}}
		if r.Simple {
			s.env["CLAUDE_CODE_SIMPLE"] = "1"
		}
		var result SessionStartResult
		err = owner.Run(ctx, func(scope *cooperative.Scope) error {
			var failure error
			result, failure = s.ExecuteSessionStart(ctx, NativeSessionStartInput{SessionID: "actual-session", Source: r.Source, AgentType: r.AgentType, Model: r.Model, Call: Context{Scope: scope}})
			return failure
		})
		owner.Close()
		if err != nil {
			t.Fatal(i, err)
		}
		for _, message := range result.Messages {
			message.Delete("uuid")
			message.Delete("timestamp")
			message.Set("toolUseID", nil)
			message.Set("parentToolUseID", nil)
			if attachment, ok := message.Object("attachment"); ok {
				attachment.Set("toolUseID", nil)
			}
		}
		for _, message := range source.Rows[i].Messages {
			message.Set("toolUseID", nil)
			message.Set("parentToolUseID", nil)
			if attachment, ok := message.Object("attachment"); ok {
				attachment.Set("toolUseID", nil)
			}
		}
		actual := commandJSON(map[string]any{"messages": result.Messages, "initialUserMessage": result.InitialUserMessage, "watchPaths": result.WatchPaths})
		expected := commandJSON(map[string]any{"messages": source.Rows[i].Messages, "initialUserMessage": source.Rows[i].InitialUserMessage, "watchPaths": source.Rows[i].WatchPaths})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(expected, &b)
		if !reflect.DeepEqual(a, b) || bytes.Count(actual, []byte(`\ud800`)) != bytes.Count(expected, []byte(`\ud800`)) {
			t.Errorf("original SessionStart%d\nGo%s\nSource%s", i, actual, expected)
		}
		if !r.Simple && string(r.Settings.Get("disableAllHooks")) != "true" {
			a, _ := actualInput.MarshalJSON()
			b, _ := source.Rows[i].Input.MarshalJSON()
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b)) {
				t.Errorf("original SessionStart input%d Go%s Source%s", i, a, b)
			}
		}
	}
}
