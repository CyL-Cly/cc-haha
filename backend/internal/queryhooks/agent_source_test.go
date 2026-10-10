package queryhooks

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
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agenthook"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestAgentHookExecutorAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []protocol.JSONObject{}
	hook := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("agent")}, protocol.JSONField{Name: "prompt", Value: commandJSON("Verify $ARGUMENTS")}, protocol.JSONField{Name: "model", Value: commandJSON("fixture-model")}, protocol.JSONField{Name: "timeout", Value: commandJSON(1)})
	attachment := func(raw string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("attachment")}, protocol.JSONField{Name: "attachment", Value: commandJSON(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("structured_output")}, protocol.JSONField{Name: "data", Value: json.RawMessage(raw)}))})
	}
	assistant := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("assistant")}, protocol.JSONField{Name: "message", Value: commandJSON(map[string]any{"content": []map[string]any{{"type": "text", "text": "check"}}})})
	for _, raw := range []string{`{"ok":true}`, `{"ok":false,"reason":"missing output"}`, `{"ok":false}`, `{"ok":true,"reason":"ignored","future":42}`, `{"ok":false,"reason":"lone\ud800 then 😀"}`, `{"ok":false,"reason":""}`, `{"ok":"yes"}`, `{"ok":false,"reason":null}`, `{}`, `null`, `[]`} {
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "hook", Value: commandJSON(hook)}, protocol.JSONField{Name: "id", Value: commandJSON("actual-tool-id")}, protocol.JSONField{Name: "messages", Value: commandJSON([]protocol.JSONObject{assistant, attachment(raw)})}))
	}
	rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "hook", Value: commandJSON(hook)}, protocol.JSONField{Name: "messages", Value: commandJSON([]protocol.JSONObject{attachment(`{"ok":null}`), attachment(`{"ok":true}`), assistant})}))
	for _, count := range []int{49, 50, 51} {
		seq := []protocol.JSONObject{}
		for i := 0; i < count; i++ {
			seq = append(seq, assistant.Clone())
		}
		seq = append(seq, attachment(`{"ok":true}`))
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "hook", Value: commandJSON(hook)}, protocol.JSONField{Name: "messages", Value: commandJSON(seq)}))
	}
	for _, failure := range []string{"physical query failed", ""} {
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "hook", Value: commandJSON(hook)}, protocol.JSONField{Name: "error", Value: commandJSON(failure)}, protocol.JSONField{Name: "messages", Value: commandJSON([]protocol.JSONObject{})}))
	}
	timeoutHook := hook.Clone()
	timeoutHook.Set("timeout", commandJSON(.01))
	rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "hook", Value: commandJSON(timeoutHook)}, protocol.JSONField{Name: "waitAbort", Value: commandJSON(true)}))
	r := rows[0].Clone()
	r.Set("child", commandJSON(true))
	rows = append(rows, r)
	r = rows[0].Clone()
	h := hook.Clone()
	h.Delete("model")
	h.Set("prompt", commandJSON("Verify without placeholder"))
	r.Set("hook", commandJSON(h))
	rows = append(rows, r)
	r = rows[0].Clone()
	r.Set("parentAborted", commandJSON(true))
	rows = append(rows, r)
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/agent-hook.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AGENT_HOOK_FIXTURE=1", "PATH=/usr/bin:/bin", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"home": home, "rows": rows}))
	data, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(data))
	}
	var source struct {
		Rows []struct {
			Result                   protocol.JSONObject
			Captured                 protocol.JSONObject
			Enforcement              protocol.JSONObject
			Yields, Returned, Leaked int
		}
	}
	if e = json.Unmarshal(data, &source); e != nil {
		t.Fatal(e, string(data))
	}
	for i, row := range rows {
		owner := cooperative.New()
		factory, _ := messages.NewFactory(func() string { return "fixture" }, func() time.Time { return time.Unix(0, 0) })
		h, _ := row.Object("hook")
		clearCount, yields := 0, 0
		var captured AgentModelInput
		s := &Service{options: Options{UUID: func() string { return "fixture" }, Commands: &CommandBindings{Owner: owner, Factory: factory}, Agents: &AgentBindings{Owner: owner, Transcript: func(*cooperative.Scope, Context) (string, error) {
			if string(row.Get("child")) == "true" {
				return "/original/child/transcript.jsonl", nil
			}
			return "/original/main/transcript.jsonl", nil
		}, Model: func(_ *cooperative.Scope, h protocol.JSONObject) (string, error) {
			if h.Get("model") == nil {
				return "small-fast-model", nil
			}
			return h.String("model"), nil
		}, Clear: func(*cooperative.Scope, string) error { clearCount++; return nil }, Query: func(ctx context.Context, _ *cooperative.Scope, in AgentModelInput, receive func(queryloop.Message) (bool, error)) error {
			captured = in
			if string(row.Get("waitAbort")) == "true" {
				<-ctx.Done()
				return ctx.Err()
			}
			if row.String("error") != "" {
				return errors.New(row.String("error"))
			}
			var seq []protocol.JSONObject
			_ = json.Unmarshal(row.Get("messages"), &seq)
			for _, record := range seq {
				yields++
				next, err := receive(queryloop.Message{Record: record})
				if err != nil {
					return err
				}
				if !next {
					return nil
				}
			}
			return nil
		}}}}
		callCtx := context.Background()
		if string(row.Get("parentAborted")) == "true" {
			var c context.CancelFunc
			callCtx, c = context.WithCancel(callCtx)
			c()
		}
		var actual protocol.JSONObject
		e = owner.Run(ctx, func(scope *cooperative.Scope) error {
			result, err := s.executeNativeAgentHook(callCtx, Context{Scope: scope, HookName: "Stop", HookEvent: "Stop", ToolUseContext: &struct{}{}}, h, protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: commandJSON("actual-session")}), row.String("id"))
			if err != nil {
				return err
			}
			actual = protocol.NewJSONObject(protocol.JSONField{Name: "hook", Value: commandJSON(h)}, protocol.JSONField{Name: "outcome", Value: commandJSON(result.Outcome)})
			if result.Payload != nil {
				actual.Set("message", commandJSON(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("attachment")}, protocol.JSONField{Name: "attachment", Value: commandJSON(result.Payload)})))
			}
			if result.BlockingErrorRaw != nil {
				actual.Set("blockingError", commandJSON(protocol.NewJSONObject(protocol.JSONField{Name: "blockingError", Value: result.BlockingErrorRaw}, protocol.JSONField{Name: "command", Value: commandJSON(result.Command)})))
			}
			return nil
		})
		owner.Close()
		if e != nil {
			t.Fatal(i, e)
		}
		var a, b any
		_ = json.Unmarshal(commandJSON(actual), &a)
		_ = json.Unmarshal(commandJSON(source.Rows[i].Result), &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("row%d Go%s Source%s", i, commandJSON(actual), commandJSON(source.Rows[i].Result))
		}
		if yields != source.Rows[i].Yields {
			t.Errorf("row%d actual yields=%d Source=%d", i, yields, source.Rows[i].Yields)
		}
		if clearCount != 1-source.Rows[i].Leaked {
			t.Errorf("row%d actual clear=%d Source leaked=%d", i, clearCount, source.Rows[i].Leaked)
		}
		content, _ := captured.Prompt.Object("message")
		if !bytes.Equal(content.Get("content"), source.Rows[i].Captured.Get("content")) {
			t.Errorf("row%d prompt Go%s Source%s", i, content.Get("content"), source.Rows[i].Captured.Get("content"))
		}
		var prompt []string
		_ = json.Unmarshal(source.Rows[i].Captured.Get("systemPrompt"), &prompt)
		if len(prompt) != 1 || prompt[0] != captured.SystemPrompt || source.Rows[i].Captured.String("model") != captured.Model || source.Rows[i].Captured.String("agentID") != captured.HookAgentID {
			t.Errorf("row%d actual model/prompt/agent differs", i)
		}
		if source.Rows[i].Captured.String("structuredPrompt") != agenthook.StructuredPrompt {
			t.Fatal("original StructuredOutput prompt drift")
		}
		parent := protocol.NewJSONObject()
		_ = parent.UnmarshalJSON([]byte(`{"mode":"default","alwaysAllowRules":{"session":["Read(/already)"],"cliArg":["Bash(echo *)"]}}`))
		original := string(commandJSON(parent))
		permission, err := agenthook.PermissionContext(parent, captured.TranscriptPath)
		if err != nil {
			t.Fatal(err)
		}
		var permissionA, permissionB any
		_ = json.Unmarshal(commandJSON(permission), &permissionA)
		_ = json.Unmarshal(source.Rows[i].Captured.Get("permission"), &permissionB)
		if !reflect.DeepEqual(permissionA, permissionB) || string(commandJSON(parent)) != original || string(source.Rows[i].Captured.Get("parentPermissionUnchanged")) != "true" {
			t.Fatal("actual parent permission clone drift", i, string(commandJSON(permission)), string(source.Rows[i].Captured.Get("permission")), original, string(commandJSON(parent)), string(source.Rows[i].Captured.Get("parentPermissionUnchanged")))
		}
		names := []string{}
		for _, name := range []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep", "TaskOutput", "ExitPlanMode", "EnterPlanMode", "Agent", "AskUserQuestion", "TaskStop", "Workflow", "StructuredOutput", "mcp__peer__echo"} {
			if agenthook.ToolAllowed(name, "") {
				names = append(names, name)
			}
		}
		names = append(names, "StructuredOutput")
		var sourceNames []string
		_ = json.Unmarshal(source.Rows[i].Captured.Get("tools"), &sourceNames)
		if !reflect.DeepEqual(names, sourceNames) {
			t.Fatal("actual Source hook tool filtering", names, sourceNames)
		}
		selector, err := modelselection.NewParsing(modelselection.Options{Environment: map[string]string{"ANTHROPIC_MODEL": "opusplan"}, Settings: protocol.NewJSONObject()})
		if err != nil {
			t.Fatal(err)
		}
		selected, err := selector.RuntimeModel(commandJSON(captured.Model), "dontAsk", false)
		if err != nil || !bytes.Equal(selected, source.Rows[i].Captured.Get("runtimeModel")) {
			t.Fatal("actual dontAsk runtime model", selected, err)
		}
		if messages.DontAskRejectMessage("Write") != source.Rows[i].Captured.String("dontAskRejection") {
			t.Fatal("Source dontAsk rejection drift")
		}
		var parseSource []protocol.JSONObject
		_ = json.Unmarshal(source.Rows[i].Captured.Get("parses"), &parseSource)
		var seq []protocol.JSONObject
		_ = json.Unmarshal(row.Get("messages"), &seq)
		parseIndex := 0
		for _, message := range seq {
			a, _ := message.Object("attachment")
			if message.String("type") != "attachment" || a.String("type") != "structured_output" {
				continue
			}
			raw := a.Get("data")
			var input protocol.JSONObject
			var parsed protocol.JSONObject
			var err error
			if err = input.UnmarshalJSON(raw); err != nil || !input.IsObject() {
				err = toolschema.ObjectInputValidationError(raw)
			} else {
				parsed, err = toolschema.ParseAgentHookOutput(input)
			}
			actualParse := protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: commandJSON(err == nil)})
			if err == nil {
				actualParse.Set("data", commandJSON(parsed))
			} else {
				var validation *toolschema.ValidationError
				if !errors.As(err, &validation) {
					t.Fatal(err)
				}
				actualParse.Set("issues", validation.Issues)
			}
			var aParse, bParse any
			_ = json.Unmarshal(commandJSON(actualParse), &aParse)
			_ = json.Unmarshal(commandJSON(parseSource[parseIndex]), &bParse)
			if !reflect.DeepEqual(aParse, bParse) {
				t.Fatal("actual structured input schema", i, string(commandJSON(actualParse)), string(commandJSON(parseSource[parseIndex])))
			}
			parseIndex++
		}
		var schemaA, schemaB any
		_ = json.Unmarshal(agenthook.StructuredSchema, &schemaA)
		_ = json.Unmarshal(source.Rows[i].Captured.Get("schema"), &schemaB)
		if !reflect.DeepEqual(schemaA, schemaB) {
			t.Fatal("original hook schema drift")
		}
		if source.Rows[i].Enforcement.String("type") != "function" || string(source.Rows[i].Enforcement.Get("timeout")) != "5000" || string(source.Rows[i].Enforcement.Get("empty")) != "false" || string(source.Rows[i].Enforcement.Get("successful")) != "true" {
			t.Fatal("original structured-output enforcement", fmt.Sprint(source.Rows[i].Enforcement))
		}
	}
}
