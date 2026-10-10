package queryhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/httphook"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestNativeOutsideHooksMatchingOrderOutputsSchemaAndTimeoutSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		Event      string                     `json:"event"`
		SID        string                     `json:"sid"`
		MatchQuery string                     `json:"matchQuery"`
		Fields     protocol.JSONObject        `json:"fields"`
		Settings   protocol.JSONObject        `json:"settings"`
		Outputs    map[string]map[string]any  `json:"outputs"`
		HTTP       map[string]httphook.Result `json:"http"`
		Simple     bool                       `json:"simple"`
		Aborted    bool                       `json:"aborted"`
		Wrapper    bool                       `json:"wrapper"`
	}
	rows := []row{}
	for _, output := range []map[string]any{{"stdout": "plain"}, {"stdout": "{invalid", "code": 2, "stderr": "exit-two"}, {"stdout": `{"decision":"block","systemMessage":"note\ud800","hookSpecificOutput":{"hookEventName":"FileChanged","watchPaths":["/old/file"]}}`}, {"stdout": `{"continue":"bad"}`}, {"stdout": `{"async":true,"asyncTimeout":2000}`}, {"aborted": true}} {
		settings := protocol.NewJSONObject()
		_ = settings.UnmarshalJSON([]byte(`{"hooks":{"SessionEnd":[{"matcher":"prompt_input_exit","hooks":[{"type":"command","command":"one"},{"type":"prompt","prompt":"source prompt"},{"type":"agent","prompt":"source agent"},{"type":"http","url":"http://127.0.0.1/original"},{"type":"command","command":"one","statusMessage":"last"},{"type":"command","command":"skipped","if":"Read(*)"}]},{"matcher":"never","hooks":[{"type":"command","command":"never"}]}]}}`))
		rows = append(rows, row{Event: "SessionEnd", SID: "actual-sid", MatchQuery: "prompt_input_exit", Fields: protocol.NewJSONObject(protocol.JSONField{Name: "reason", Value: commandJSON("prompt_input_exit")}), Settings: settings, Outputs: map[string]map[string]any{"one": output}, HTTP: map[string]httphook.Result{"http://127.0.0.1/original": {OK: true, Body: commandJSON(`{"decision":"block"}`)}}})
	}
	for _, body := range []string{"", `{"async":true,"asyncTimeout":10}`, "not JSON", `{"continue":"bad"}`, `{"hookSpecificOutput":{"hookEventName":"WorktreeCreate","worktreePath":"/actual/tree"}}`} {
		r := rows[0]
		r.Settings = protocol.NewJSONObject()
		_ = r.Settings.UnmarshalJSON([]byte(`{"hooks":{"WorktreeCreate":[{"hooks":[{"type":"http","url":"http://127.0.0.1/original"}]}]}}`))
		r.Event = "WorktreeCreate"
		r.MatchQuery = ""
		r.Fields = protocol.NewJSONObject()
		r.HTTP = map[string]httphook.Result{"http://127.0.0.1/original": {OK: true, Body: commandJSON(body)}}
		rows = append(rows, r)
	}
	r := rows[0]
	r.Aborted = true
	rows = append(rows, r)
	r = rows[0]
	r.Simple = true
	rows = append(rows, r)
	for _, source := range []string{"skills", "user_settings", "project_settings", "local_settings", "policy_settings"} {
		r := rows[0]
		r.Event = "ConfigChange"
		r.Wrapper = true
		r.MatchQuery = source
		r.Fields = protocol.NewJSONObject(protocol.JSONField{Name: "source", Value: commandJSON(source)}, protocol.JSONField{Name: "file_path", Value: commandJSON("/original/config")})
		r.Settings = protocol.NewJSONObject()
		_ = r.Settings.UnmarshalJSON([]byte(`{"hooks":{"ConfigChange":[{"matcher":"skills|user_settings|project_settings|local_settings|policy_settings","hooks":[{"type":"command","command":"one"}]}]}}`))
		r.Outputs = map[string]map[string]any{"one": {"stdout": `{"decision":"block"}`}}
		rows = append(rows, r)
	}
	times := []string{"", "0", "-1", "1", " +25tail", "0x20", "1e5", "Infinity", "99999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999"}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-outside-hooks.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OUTSIDE_HOOK_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"homeDir": home, "rows": rows, "timeouts": times}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Rows     [][]OutsideResult
		Timeouts []float64
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	for i, r := range rows {
		owner := cooperative.New()
		settings := r.Settings.Clone()
		s := &Service{options: Options{UUID: func() string { return "fixture" }, MainSessionID: r.SID, MergedSettings: func() (*protocol.JSONObject, error) { return &settings, nil }}, env: map[string]string{}}
		if r.Simple {
			s.env["CLAUDE_CODE_SIMPLE"] = "1"
		}
		s.options.Commands = &CommandBindings{Owner: owner, Input: func(_ context.Context, c Context) (protocol.JSONObject, error) {
			input := protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: commandJSON(r.SID)}, protocol.JSONField{Name: "transcript_path", Value: commandJSON("/original/transcript")}, protocol.JSONField{Name: "cwd", Value: commandJSON("/original/cwd")}, protocol.JSONField{Name: "hook_event_name", Value: commandJSON(r.Event)})
			for _, field := range c.InputFields.OwnProperties() {
				_ = input.SetPropertyJSON(field.NameJSON, field.Value)
			}
			return input, nil
		}, Run: func(_ context.Context, _ Context, h, _ protocol.JSONObject) (CommandOutput, error) {
			o := r.Outputs[h.String("command")]
			out := CommandOutput{}
			if text, ok := o["stdout"].(string); ok {
				out.Stdout = text
			}
			if text, ok := o["stderr"].(string); ok {
				out.Stderr = text
			}
			if code, ok := o["code"].(int); ok {
				out.Code = code
			}
			out.Aborted = o["aborted"] == true
			return out, nil
		}}
		s.options.HTTP = &HTTPBindings{Owner: owner, Run: func(_ context.Context, _ Context, h, _ protocol.JSONObject) (httphook.Result, error) {
			return r.HTTP[h.String("url")], nil
		}}
		ctx := context.Background()
		if r.Aborted {
			c, cancel := context.WithCancel(ctx)
			cancel()
			ctx = c
		}
		var actual []OutsideResult
		err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			var e error
			if r.Wrapper {
				actual, e = s.ExecuteConfigChange(ctx, r.Fields.String("source"), r.Fields.Get("file_path"), Context{Scope: scope})
			} else {
				actual, e = s.ExecuteOutside(ctx, OutsideInput{Event: r.Event, SessionID: r.SID, MatchQuery: r.MatchQuery, Fields: r.Fields, Call: Context{Scope: scope}})
			}
			return e
		})
		owner.Close()
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		var a, b any
		_ = json.Unmarshal(commandJSON(actual), &a)
		_ = json.Unmarshal(commandJSON(source.Rows[i]), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("row %d actual=%s Source=%s", i, commandJSON(actual), commandJSON(source.Rows[i]))
		}
	}
	for i, raw := range times {
		if float64(SessionEndTimeout(raw)/time.Millisecond) != source.Timeouts[i] {
			t.Fatal("Source timeout", raw, SessionEndTimeout(raw), source.Timeouts[i])
		}
	}
}
