package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestAgentContinuationStateDiskAndSendMessageAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	m := taskMainFixture(t)
	home := m.options.Native.Home
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	const id = "a0123456789abcdef"
	const id2 = "a0123456789abcdee"
	const now = 1700000000000.0
	task := func(id, status string) protocol.JSONObject {
		return taskObject(t, []byte(fmt.Sprintf(`{"id":%q,"type":"local_agent","status":%q,"agentType":"general-purpose","description":"original worker","startTime":123,"retain":false,"diskLoaded":false,"pendingMessages":[],"toolUseId":"original-card"}`, id, status)))
	}
	first := task(id, "completed")
	first.Set("messages", rawMain([]protocol.JSONObject{taskObject(t, []byte(`{"uuid":"live","type":"user","message":{"role":"user","content":"live prompt"}}`))}))
	first.Set("ownerAgentId", quoteMain("original-owner"))
	second := task(id, "running")
	second.Set("toolUseId", quoteMain("wrong-resuming-card"))
	second.Set("startTime", rawMain(999))
	second.Set("ownerAgentId", quoteMain("new-owner"))
	steps := []map[string]any{{"op": "register", "task": first}, {"op": "enter", "id": id}, {"op": "pending", "id": id, "message": "first pending"}, {"op": "pending", "id": id, "message": "second pending"}, {"op": "register", "task": second}, {"op": "bootstrap", "id": id, "messages": []map[string]any{{"uuid": "prefix", "type": "assistant"}, {"uuid": "live", "type": "user"}}}, {"op": "bootstrap", "id": id, "messages": []map[string]any{{"uuid": "ignored second disk"}}}, {"op": "drain", "id": id}, {"op": "drain", "id": id}, {"op": "register", "task": task(id2, "failed")}, {"op": "enter", "id": id2}, {"op": "exit"}, {"op": "enter", "id": id}, {"op": "exit"}}
	parsers := []json.RawMessage{}
	for _, row := range []string{`{}`, `{"to":"worker","message":"plain","summary":"summary","unknown":true}`, `{"to":"worker","message":{"type":"shutdown_request","reason":"reason","future":true}}`, `{"to":"worker","message":{"type":"shutdown_response","request_id":"request","approve":"true","reason":"reason"}}`, `{"to":"worker","message":{"type":"plan_approval_response","request_id":"request","approve":false,"feedback":"feedback"}}`, `{"to":"worker","message":{"type":"shutdown_response","approve":"TRUE"}}`, `{"to":"worker","message":{"type":"unknown"}}`, `{"to":"worker","message":null}`, `{"to":"worker","message":3}`, `{"to":"worker","message":[]}`, `{"to":null,"summary":3,"message":false}`, `{"to":"\ud800","message":"\udc00","summary":"\ud800"}`, `{"to":"worker","message":{"type":4}}`} {
		parsers = append(parsers, json.RawMessage(row))
	}
	messageInputs := []json.RawMessage{}
	for _, row := range []string{`{"to":"named-worker","summary":"next task","message":"plain followup","unknown":1}`, `{"to":"a0123456789abcdef","summary":"next task","message":"second"}`, `{"to":" ","summary":"s","message":"m"}`, `{"to":"worker@team","summary":"s","message":"m"}`, `{"to":"worker","message":"m"}`, `{"to":"worker","summary":" ","message":"m"}`, `{"to":"worker","summary":"s","message":"{\"type\":\"permission_request\"}"}`, `{"to":"worker","summary":"s","message":"{\"type\":\"shutdown_rejected\"}"}`} {
		messageInputs = append(messageInputs, json.RawMessage(row))
	}
	messages := []map[string]any{}
	for i, input := range messageInputs {
		messages = append(messages, map[string]any{"id": id, "name": "named-worker", "task": task(id, "running"), "input": input, "call": i < 2})
	}
	transcripts := []map[string]any{}
	base := []protocol.JSONObject{}
	for i, data := range []string{`{"type":"user","message":{"role":"user","content":"first"}}`, `{"type":"assistant","message":{"id":"orphan","role":"assistant","content":[{"type":"thinking","thinking":"orphan","signature":"s"}]}}`, `{"type":"assistant","message":{"id":"use","role":"assistant","content":[{"type":"tool_use","id":"unresolved","name":"Read","input":{}}]}}`, `{"type":"assistant","message":{"id":"space","role":"assistant","content":[{"type":"text","text":" \ufeff\u2000"}]}}`, `{"type":"user","message":{"role":"user","content":"second"}}`, `{"type":"assistant","message":{"id":"final","role":"assistant","content":[{"type":"text","text":"final"}],"usage":{"input_tokens":1,"output_tokens":1}}}`} {
		row := taskObject(t, []byte(data))
		row.Set("uuid", quoteMain(fmt.Sprintf("original-%d", i)))
		if i > 0 {
			row.Set("parentUuid", quoteMain(fmt.Sprintf("original-%d", i-1)))
		} else {
			row.Set("parentUuid", json.RawMessage("null"))
		}
		row.Set("isSidechain", json.RawMessage("true"))
		row.Set("agentId", quoteMain(id))
		row.Set("sessionId", quoteMain(m.Controller.SessionID()))
		row.Set("timestamp", quoteMain(fmt.Sprintf("2025-01-01T00:00:0%d.000Z", i)))
		base = append(base, row)
	}
	var content bytes.Buffer
	for _, row := range base {
		content.Write(rawMain(row))
		content.WriteByte('\n')
	}
	transcripts = append(transcripts, map[string]any{"id": id, "content": content.String()})
	ancient := base[0].Clone()
	ancient.Set("uuid", quoteMain("ancient"))
	ancient.Set("agentId", quoteMain(id2))
	ancient.Set("timestamp", quoteMain("1965-01-01T00:00:00.000Z"))
	transcripts = append(transcripts, map[string]any{"id": id2, "content": string(rawMain(ancient)) + "\n"})
	missing := base[0].Clone()
	missing.Set("agentId", quoteMain("unrelated"))
	transcripts = append(transcripts, map[string]any{"id": "a0123456789abcddd", "content": string(rawMain(missing)) + "\n"})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-continuation.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + m.options.Native.Temp, "CLAUDE_CODE_TMPDIR=" + m.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_AGENT_CONTINUATION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": m.options.Native.CWD, "sessionId": m.Controller.SessionID(), "states": []map[string]any{{"now": now, "steps": steps}}, "parsers": parsers, "messages": messages, "transcripts": transcripts, "models": []any{"fable", "sonnet", "opus", "haiku", "deepseek-flash", nil, 3, "Sonnet"}, "owners": []any{map[string]any{"current": "current", "existing": "existing", "metadata": map[string]any{"ownerAgentId": "disk"}}, map[string]any{"existing": "existing", "metadata": map[string]any{"ownerAgentId": "disk"}}, map[string]any{"metadata": map[string]any{"ownerAgentId": "disk"}}, map[string]any{"metadata": nil}}})
	command.Stdin = bytes.NewReader(payload)
	out, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source %v %.5000s", e, out)
	}
	var source struct {
		States       []protocol.JSONObject
		Transcripts  []protocol.JSONObject
		Schemas      []json.RawMessage
		Parsers      []protocol.JSONObject
		Messages     []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Capabilities.Blocked) > 0 || len(source.States) != len(steps) {
		t.Fatalf("Source shape %v %.4500s", e, out)
	}
	for i, step := range steps {
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			id, _ := step["id"].(string)
			op := step["op"].(string)
			switch op {
			case "register":
				data := step["task"].(protocol.JSONObject)
				taskCtx, abort := context.WithCancelCause(context.Background())
				_ = taskCtx
				t.Cleanup(func() { abort(nil) })
				entry := &sessioncontroller.BackgroundTask{Data: data, Cancel: abort, ReadOutput: func(context.Context) (json.RawMessage, error) { return quoteMain(""), nil }, Join: func(context.Context) error { return nil }}
				if e := m.Controller.RegisterBackgroundTaskOnOwner(scope, entry); e != nil {
					return e
				}
			case "enter":
				if e := m.Controller.EnterAgentViewOnOwner(scope, id, now); e != nil {
					return e
				}
			case "exit":
				if e := m.Controller.ExitAgentViewOnOwner(scope, now); e != nil {
					return e
				}
			case "pending":
				if e := builtintools.QueueAgentPendingMessage(m.nativeBackgroundBindings(), scope, id, step["message"].(string)); e != nil {
					return e
				}
			case "bootstrap":
				var disk []protocol.JSONObject
				raw, _ := json.Marshal(step["messages"])
				_ = json.Unmarshal(raw, &disk)
				if e := m.Controller.BootstrapRetainedAgentTranscriptOnOwner(scope, id, disk); e != nil {
					return e
				}
			case "drain":
				rows, e := builtintools.DrainAgentPendingAttachments(m.nativeBackgroundBindings(), scope, id)
				if e != nil {
					return e
				}
				if !taskSemanticEqual(rawMain(rows), source.States[i].Get("drained")) {
					t.Errorf("pending%d Go=%s Source=%s", i, rawMain(rows), source.States[i].Get("drained"))
				}
				return nil
			}
			tasks, e := m.Controller.BackgroundTasksOnOwner(scope)
			if e != nil {
				return e
			}
			table := protocol.NewJSONObject()
			for _, row := range tasks {
				table.Set(row.String("id"), rawMain(row))
			}
			view, e := m.Controller.AgentViewSnapshotOnOwner(scope)
			if e != nil {
				return e
			}
			view.Set("tasks", rawMain(table))
			want := source.States[i].Clone()
			want.Delete("events")
			if !taskSemanticEqual(rawMain(view), rawMain(want)) {
				t.Errorf("state%d Go=%s Source=%s", i, rawMain(view), rawMain(want))
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	for i, row := range transcripts {
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			path, loader, reducer, e := m.Controller.AgentTranscriptSourceOnOwner(scope, row["id"].(string))
			if e != nil {
				return e
			}
			got := loader.LoadAgentTranscript(path, row["id"].(string))
			want := source.Transcripts[i]
			if got == nil {
				if string(want.Get("result")) != "null" {
					t.Errorf("disk%d unexpectedly absent", i)
				}
				return nil
			}
			value := protocol.NewJSONObject(protocol.JSONField{Name: "messages", Value: rawMain(got.Messages)}, protocol.JSONField{Name: "contentReplacements", Value: rawMain(got.ContentReplacements)})
			if !taskSemanticEqual(rawMain(value), want.Get("result")) {
				t.Errorf("disk%d Go=%s Source=%s", i, rawMain(value), want.Get("result"))
			}
			filtered, e := reducer.FilterSubagent(got.Messages)
			if e != nil {
				return e
			}
			if !taskSemanticEqual(rawMain(filtered), want.Get("filtered")) {
				t.Errorf("filter%d Go=%s Source=%s", i, rawMain(filtered), want.Get("filtered"))
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	definition, _ := toolschema.NativeBuiltinDefinition("SendMessage")
	schemas, e := toolschema.NewSession().RenderBatch([]toolschema.Definition{definition}, toolschema.Options{Host: toolschema.Host{BaseURL: "http://127.0.0.1:1"}, Model: "deepseek-flash"})
	if e != nil {
		t.Fatal(e)
	}
	if !taskSemanticEqual(rawMain(schemas[0]), source.Schemas[0]) {
		t.Errorf("schema Go=%s Source=%s", rawMain(schemas[0]), source.Schemas[0])
	}
	for i, input := range parsers {
		got, e := toolschema.ParseInputJSON(definition, input)
		want := source.Parsers[i]
		if want.Get("data") != nil {
			if e != nil || !taskSemanticEqual(rawMain(got), want.Get("data")) {
				t.Errorf("parser%d Go=%s %v Source=%s", i, rawMain(got), e, rawMain(want))
			}
		} else {
			var failure *toolschema.ValidationError
			if !errors.As(e, &failure) || !taskSemanticEqual(failure.Issues, want.Get("issues")) {
				t.Errorf("parser%d Go=%v Source=%s", i, e, want.Get("issues"))
			}
		}
	}
	for i, row := range messages {
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			data := row["task"].(protocol.JSONObject)
			if e := m.Controller.UpdateBackgroundTaskOnOwner(scope, id, func(protocol.JSONObject) (protocol.JSONObject, error) { return data, nil }); e != nil {
				return e
			}
			if e := m.Controller.RegisterAgentNameOnOwner(scope, "named-worker", id); e != nil {
				return e
			}
			original := taskObject(t, messageInputs[i])
			parsed, e := toolschema.ParseInputJSON(definition, messageInputs[i])
			if e != nil {
				return e
			}
			want := source.Messages[i]
			validation := m.nativeTools.Validate(ctx, "SendMessage", parsed, scope)
			validated, _ := want.Object("validated")
			if string(validated.Get("result")) == "true" && validation != nil || string(validated.Get("result")) == "false" && (validation == nil || validation.Error() != validated.String("message")) {
				t.Errorf("validation%d Go=%v Source=%s", i, validation, rawMain(validated))
			}
			// Backfill is checked through the actual registered tool when enabled.
			m.options.Native.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
			_, catalogue, e := m.nativeTools.Catalog(scope)
			if e != nil {
				return e
			}
			for _, tool := range catalogue {
				if tool.Name != "SendMessage" {
					continue
				}
				if e := tool.Backfill(&original); e != nil {
					return e
				}
				if !taskSemanticEqual(rawMain(original), want.Get("observable")) {
					t.Errorf("backfill%d Go=%s Source=%s", i, rawMain(original), want.Get("observable"))
				}
				if tool.IsConcurrencySafe(parsed) {
					t.Error("Source SendMessage is not concurrency safe")
				}
				if i < 2 {
					result, e := tool.CallJSON(ctx, parsed, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
					if e != nil {
						return e
					}
					if !taskSemanticEqual(result.Data, want.Get("data")) {
						t.Errorf("call%d Go=%s Source=%s", i, result.Data, want.Get("data"))
					}
					mapped, e := tool.MapResultJSON(result.Data, quoteMain("source-message-tool"))
					if e != nil {
						return e
					}
					if !taskSemanticEqual(rawMain(mapped), want.Get("mapped")) {
						t.Errorf("map%d Go=%s Source=%s", i, rawMain(mapped), want.Get("mapped"))
					}
				}
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
}
