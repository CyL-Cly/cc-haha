package sessioninbox

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

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func input(id, sender, text string) protocol.JSONObject {
	return object("subtype", "enqueue_session_message", "message_id", id, "sender_session_id", sender, "text", text)
}
func tree(t *testing.T, data json.RawMessage) any {
	t.Helper()
	if len(data) > 0 && data[0] == '{' {
		o := parsed(data)
		out := map[string]any{}
		for _, f := range o.OwnProperties() {
			key, e := protocol.ParseAndStringifyJavaScriptJSON(f.NameJSON)
			if e != nil {
				t.Fatal(e)
			}
			out[string(key)] = tree(t, f.Value)
		}
		return out
	}
	if len(data) > 0 && data[0] == '[' {
		var a []json.RawMessage
		if e := json.Unmarshal(data, &a); e != nil {
			t.Fatal(e)
		}
		out := []any{}
		for _, v := range a {
			out = append(out, tree(t, v))
		}
		return out
	}
	b, e := protocol.ParseAndStringifyJavaScriptJSON(data)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func compare(t *testing.T, got any, want json.RawMessage) {
	t.Helper()
	if !reflect.DeepEqual(tree(t, raw(got)), tree(t, want)) {
		t.Fatalf("Source differs\nGo: %s\nSource: %s", raw(got), want)
	}
}
func parseObservation(def toolschema.Definition, in json.RawMessage) protocol.JSONObject {
	v, e := toolschema.ParseInputJSON(def, in)
	if e != nil {
		if validation, ok := e.(*toolschema.ValidationError); ok {
			return object("issues", validation.Issues)
		}
		return object("error", e.Error())
	}
	return object("value", v)
}

func TestActualSessionInboxSchemasConsumptionAndQueueMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	valid := input("one", "sender", "text")
	inputs := []json.RawMessage{raw(valid), raw(nil), raw([]any{}), raw(object()), raw(input("", "sender", "text")), raw(input("one", "", "text")), raw(input("one", "sender", "")), raw(input(strings.Repeat("x", 201), "sender", "text")), raw(input("one", strings.Repeat("😀", 101), "text")), raw(input("one", "sender", strings.Repeat("x", 32001)))}
	for _, key := range []string{"subtype", "message_id", "sender_session_id", "text", "start_if_idle"} {
		for _, value := range []any{nil, false, true, 5, object("future", true)} {
			v := parsed(raw(valid))
			v.Set(key, raw(value))
			inputs = append(inputs, raw(v))
		}
	}
	unknown := parsed(raw(valid))
	unknown.Set("future", raw(true))
	inputs = append(inputs, raw(unknown))
	emptyID := parsed(raw(valid))
	emptyID.Set("message_id", []byte(`"\ud800"`))
	inputs = append(inputs, raw(emptyID))
	ids := []json.RawMessage{raw(""), raw("one"), raw("中文😀"), []byte(`"\ud800"`), []byte(`"\ufffd"`), raw(nil), raw(false), raw(1e100), raw([]any{"one", nil, "two"}), raw(object("ordinary", true))}
	queued := []protocol.JSONObject{}
	for _, payload := range []protocol.JSONObject{object("type", "queued_command", "prompt", FormatPrompt(raw("peer"), raw("one"), raw("text")), "source_uuid", MessageUUID(raw("one")), "origin", object("kind", "channel", "server", "session-collaboration")), object("type", "queued_command", "prompt", "task", "commandMode", "task-notification")} {
		queued = append(queued, object("type", "attachment", "attachment", payload, "uuid", "queued-id", "timestamp", "2026-01-01T00:00:00Z"))
	}
	toolInputs := []protocol.JSONObject{}
	for _, name := range []string{"ListSessions", "ReadSession", "CreateSession", "SendSessionMessage", "WaitSessions"} {
		for _, v := range []protocol.JSONObject{object(), object("unknown", true), object("limit", 0), object("query", strings.Repeat("😀", 251)), object("sessionId", "one", "includeOutputs", true, "maxOutputCharsPerItem", 99), object("sessionId", "one", "limit", 1.5), object("prompt", "task", "providerId", nil), object("targetSessionId", "one", "content", strings.Repeat("x", 32001)), object("sessionIds", []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}), object("sessionIds", []any{"a", nil}), object("afterRevision", 0, "timeoutMs", 300001), object("afterRevision", 9007199254740992)} {
			toolInputs = append(toolInputs, object("name", name, "input", v))
		}
	}
	accept := func(in protocol.JSONObject) protocol.JSONObject { return object("action", "accept", "input", in) }
	cases := []protocol.JSONObject{
		object("operations", []protocol.JSONObject{accept(valid), accept(valid), object("action", "consume", "messageId", "one"), accept(valid), accept(input("one", "sender", "different")), object("action", "cancel")}),
		object("operations", []protocol.JSONObject{accept(valid), object("action", "cancel"), accept(input("one", "sender", "new")), object("action", "consume", "messageId", "one")}),
		object("operations", []protocol.JSONObject{object("action", "fail", "enabled", true), accept(valid), object("action", "fail", "enabled", false), accept(valid), object("action", "dispose"), accept(valid)}),
		object("history", []protocol.JSONObject{object("type", "user", "uuid", MessageUUID(raw("one")))}, "operations", []protocol.JSONObject{accept(valid)}),
		object("history", []protocol.JSONObject{object("type", "attachment", "attachment", object("type", "queued_command", "source_uuid", MessageUUID(raw("one"))))}, "operations", []protocol.JSONObject{accept(valid)}),
		object("operations", []protocol.JSONObject{object("action", "accept", "input", valid, "persisted", true)}),
	}
	many := []protocol.JSONObject{}
	for i := 0; i < 51; i++ {
		many = append(many, accept(input(strings.Repeat("x", i+1), "sender", "text")))
	}
	cases = append(cases, object("operations", many))
	home := t.TempDir()
	config, temp := filepath.Join(home, ".claude"), filepath.Join(home, "tmp")
	for _, p := range []string{config, temp} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-inbox.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SESSION_INBOX_FIXTURE=1", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(object("homeDir", home, "inputs", inputs, "toolInputs", toolInputs, "ids", ids, "cases", cases, "queued", queued)))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source %v %.2400s", e, stderr.String())
	}
	var source struct {
		Parsed, ToolRows, Schemas, Cases, Uuids []json.RawMessage
		Capabilities                            struct{ Blocked []any }
		QueuedNormalized                        []json.RawMessage
	}
	if e = json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	if len(source.Capabilities.Blocked) != 0 {
		t.Fatal("unexpected Source primitive", source.Capabilities.Blocked)
	}
	for i, v := range inputs {
		compare(t, parseObservation(toolschema.SessionMessageInputDefinition(), v), source.Parsed[i])
	}
	for i, v := range ids {
		compare(t, MessageUUID(v), source.Uuids[i])
	}
	factory, _ := messages.NewFactory(func() string { return "native-id" }, func() time.Time { return time.Unix(0, 0) })
	normalizer, e := sdkoutput.New(sdkoutput.Dependencies{Factory: factory, SessionID: func() json.RawMessage { return raw("native-session") }, NowMillis: func() float64 { return 0 }, OriginalEnv: func(string) string { return "" }})
	if e != nil {
		t.Fatal(e)
	}
	for index, message := range queued {
		decoded, e := sdkoutput.Decode(raw(message))
		if e != nil {
			t.Fatal(e)
		}
		views := []protocol.JSONObject{}
		if e = normalizer.NormalizeMessage(decoded, func(view *sdkoutput.View) error { views = append(views, view.Record); return nil }); e != nil {
			t.Fatal(e)
		}
		compare(t, views, source.QueuedNormalized[index])
	}
	for i, row := range toolInputs {
		def, e := toolschema.SessionCollaborationDefinition(row.String("name"))
		if e != nil {
			t.Fatal(e)
		}
		compare(t, parseObservation(def, row.Get("input")), source.ToolRows[i])
	}
	defs := []toolschema.Definition{}
	for _, name := range []string{"ListSessions", "ReadSession", "CreateSession", "SendSessionMessage", "WaitSessions"} {
		def, e := toolschema.SessionCollaborationDefinition(name)
		if e != nil {
			t.Fatal(e)
		}
		defs = append(defs, def)
	}
	schemas, e := toolschema.NewSession().RenderBatch(defs, toolschema.Options{Model: "deepseek-flash", Host: toolschema.Host{BaseURL: "https://api.deepseek.com/anthropic"}})
	if e != nil {
		t.Fatal(e)
	}
	for i, schema := range schemas {
		compare(t, schema, source.Schemas[i])
	}
	for n, row := range cases {
		var history []protocol.JSONObject
		_ = json.Unmarshal(row.Get("history"), &history)
		queue, receipts := []protocol.JSONObject{}, []protocol.JSONObject{}
		fail := false
		inbox, e := New(func(command protocol.JSONObject) error {
			if fail {
				return errors.New("physical queue failure")
			}
			queue = append(queue, command)
			return nil
		}, func(receipt protocol.JSONObject) error { receipts = append(receipts, receipt); return nil }, history)
		if e != nil {
			t.Fatal(e)
		}
		observations := []protocol.JSONObject{}
		var ops []protocol.JSONObject
		_ = json.Unmarshal(row.Get("operations"), &ops)
		for _, op := range ops {
			var result any
			var e error
			switch op.String("action") {
			case "accept":
				result, e = inbox.Accept(op.Get("input"), string(op.Get("persisted")) == "true")
			case "consume":
				e = inbox.Consume(MessageUUID(op.Get("messageId")))
			case "cancel":
				result, e = inbox.CancelQueued(func(predicate func(protocol.JSONObject) bool) ([]protocol.JSONObject, error) {
					removed, retained := []protocol.JSONObject{}, []protocol.JSONObject{}
					for _, v := range queue {
						if predicate(v) {
							removed = append(removed, v)
						} else {
							retained = append(retained, v)
						}
					}
					queue = retained
					return removed, nil
				})
			case "fail":
				fail = string(op.Get("enabled")) == "true"
			case "dispose":
				inbox.Dispose()
			}
			if e != nil {
				observations = append(observations, object("error", e.Error()))
			} else {
				observations = append(observations, object("result", result))
			}
		}
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			compare(t, object("observations", observations, "queue", queue, "receipts", receipts), source.Cases[n])
		})
		inbox.Dispose()
	}
}
