package sessioncollaboration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func errorObservation(e error) protocol.JSONObject {
	o := object("name", "Error", "message", e.Error())
	if a, ok := e.(*APIError); ok {
		o.Set("name", raw("ApiError"))
		o.Set("code", raw(a.Code))
		o.Set("status", raw(a.Status))
	}
	return object("error", o)
}
func resultObservation(v any, e error) protocol.JSONObject {
	if e != nil {
		return errorObservation(e)
	}
	return object("result", v)
}
func scenario(ops ...protocol.JSONObject) protocol.JSONObject { return object("operations", ops) }
func op(action string, fields ...any) protocol.JSONObject {
	out := object("action", action)
	out.Assign(object(fields...))
	return out
}
func createOp(caller, prompt string, extra ...any) protocol.JSONObject {
	in := object("prompt", prompt)
	in.Assign(object(extra...))
	return op("create", "caller", caller, "input", in)
}

func sourceCases() []protocol.JSONObject {
	rows := []protocol.JSONObject{
		scenario(op("candidates", "query", "Main"), op("state", "id", "root", "state", "blocked"), op("candidates", "query", "Main")),
		scenario(createOp("root", "a"), createOp("root", "b"), createOp("child-1", "nested"), createOp("root", "queued"), op("status"), op("state", "id", "child-1", "state", "completed", "result", "done", "eventId", "completion-1"), op("status"), op("state", "id", "child-1", "state", "completed", "result", "done", "eventId", "completion-1"), op("group", "id", "child-2")),
		scenario(op("send", "target", "existing", "content", "hello", "messageId", "stable-id"), op("send", "target", "existing", "content", "hello", "messageId", "stable-id"), op("consumed", "messageId", "stable-id"), op("send", "target", "existing", "content", "different", "messageId", "stable-id")),
		scenario(createOp("root", "a"), op("stop", "id", "root"), op("state", "id", "child-1", "state", "completed", "result", "finished"), op("restart"), op("status"), op("input", "id", "root")),
		scenario(createOp("root", "a"), op("restart"), op("status"), op("resume", "id", "child-1")),
		scenario(createOp("root", "requires permission"), op("consumed", "messageId", "00000000-0000-4000-8000-000000000002"), op("state", "id", "child-1", "state", "blocked", "result", "Awaiting approval"), op("restart"), op("status")),
		scenario(createOp("root", "task", "requestId", "retry", "title", "A title"), createOp("root", "task", "requestId", "retry", "title", "A title"), createOp("root", "different", "requestId", "retry", "title", "A title")),
		scenario(createOp("root", "task", "title", "  Explicit title  "), createOp("root", "😀 fixture prompt\nsecond line"), op("status", "ids", []string{"child-1", "child-2"})),
		scenario(createOp("missing", "no workspace"), op("send", "target", "missing", "content", "text"), createOp("root", " \uFEFF "), op("send", "target", "existing", "content", "  ")),
		scenario(createOp("root", "a"), createOp("root", "b"), op("stopGroup", "id", "child-1"), op("state", "id", "child-1", "state", "completed", "result", "stale completion"), op("status")),
		scenario(createOp("root", "a"), createOp("root", "b"), createOp("child-1", "nested"), createOp("root", "queued"), op("wait", "after", 0, "ids", []string{"child-3", "child-4"}, "timeout", 1, "caller", "child-1")),
		scenario(op("input", "id", "root", "dispatch", false), op("send", "target", "root", "content", "inbox", "messageId", "inbox-one"), op("canDeliver", "messageId", "inbox-one"), op("consumed", "messageId", "inbox-one", "target", "other"), op("consumed", "messageId", "inbox-one", "target", "root"), op("canDeliver", "messageId", "inbox-one"), op("wait", "after", 0, "timeout", 900000)),
	}
	rows[2].Set("live", raw(object("existing", "running")))
	// Source ReadSession is a current durable snapshot, not a task join. Neither
	// dispatch nor a completion status makes an uncommitted record visible.
	rows = append(rows, scenario(createOp("root", "snapshot task"),
		op("read", "id", "child-1", "options", object()),
		op("state", "id", "child-1", "state", "completed", "result", "CHILD_DURABLE_FINAL", "eventId", "done"),
		op("read", "id", "child-1", "options", object()),
		op("historyCommit", "history", object("messages", []protocol.JSONObject{object("id", "user", "type", "user", "content", "task"), object("id", "assistant", "type", "assistant", "content", "CHILD_DURABLE_FINAL")}, "page", object("historyComplete", true))),
		op("read", "id", "child-1", "options", object())))
	for _, failure := range []string{"startFailure", "enqueueFailure", "createFailure", "stopFailure"} {
		r := scenario(createOp("root", "fail", "requestId", "one"), createOp("root", "fail", "requestId", "one"), op("send", "target", "existing", "content", "retry", "messageId", "retry-id"), op("resume", "id", "existing"), op("stopGroup", "id", "root"), op("status"))
		r.Set(failure, raw("physical fixture failure"))
		if failure == "enqueueFailure" {
			r.Set("live", raw(object("child-1", "running", "existing", "running")))
		}
		rows = append(rows, r)
	}
	rows = append(rows, scenario(op("input", "id", "root"), op("recover"), op("status")))
	rows[len(rows)-1].Set("initial", raw(object("members", object(), "messages", []protocol.JSONObject{}, "futureField", object("preserve", true))))
	r := scenario(createOp("root", "original"), op("status"))
	r.Set("titles", raw(object("root", " Main renamed ", "child-1", " Indexed child ")))
	rows = append(rows, r)
	for _, value := range []json.RawMessage{raw(nil), raw([]any{}), raw(object()), raw(object("members", object(), "messages", []any{})), raw(object("version", 2, "members", object(), "messages", []any{})), raw(object("revision", -4, "members", object(), "messages", []any{}, "future", true)), raw(object("members", object("a", object("sessionId", "wrong", "rootSessionId", "a", "state", "idle")), "messages", []any{})), raw(object("members", object(), "messages", []any{object("id", "x", "sourceSessionId", "a", "targetSessionId", "b", "content", "c", "status", "bad")}))} {
		rows = append(rows, object("kind", "migration", "value", value))
	}
	for _, include := range []bool{false, true} {
		for _, limit := range []int{0, 1, 2, 10, 100} {
			for _, item := range []int{0, 100, 250, 8000} {
				m := []protocol.JSONObject{object("id", "u1", "type", "user", "content", "old request"), object("id", "a1", "type", "assistant", "content", "old result"), object("id", "u2", "type", "user", "content", "current request"), object("id", "t2", "type", "tool_result", "content", stringsOf("😀<>&", 100), "toolUseResult", object("secret", "excluded")), object("id", "a2", "type", "assistant", "content", []protocol.JSONObject{object("type", "text", "text", "answer"), object("type", "tool_result", "content", object("nested", stringsOf("😀", 300)), "tool_use_id", "call")})}
				rows = append(rows, object("kind", "projection", "value", object("messages", m, "page", object("historyComplete", true, "future", 7)), "options", object("limit", limit, "includeOutputs", include, "maxOutputCharsPerItem", item)))
			}
		}
	}
	for _, length := range []int{0, 100, 25000, 80000} {
		r := scenario(op("read", "id", "historical", "options", object("limit", 1)), op("read", "id", "historical", "options", object("includeOutputs", true, "maxOutputCharsPerItem", 100)), op("read", "id", "historical", "options", object("cursor", "broken")))
		r.Set("history", raw(object("messages", []protocol.JSONObject{object("id", "u1", "type", "user", "content", "old"), object("id", "a1", "type", "assistant", "content", "old result"), object("id", "u2", "type", "user", "content", "current"), object("id", "a2", "type", "assistant", "content", stringsOf("😀z", length))}, "page", object("sourceVersion", "physical-v1", "historyComplete", true))))
		rows = append(rows, r)
	}
	for _, length := range []int{500, 25000, 200000} {
		operations := []protocol.JSONObject{op("read", "id", "historical", "options", object("limit", 1))}
		for i := 0; i < 7; i++ {
			operations = append(operations, op("readNext", "id", "historical", "options", object("limit", 1)))
		}
		r := scenario(operations...)
		r.Set("history", raw(object("messages", []protocol.JSONObject{object("id", "u0", "type", "user", "content", "older"), object("id", "a0", "type", "assistant", "content", "older result"), object("id", "u1", "type", "user", "content", "current"), object("id", "a1", "type", "assistant", "content", stringsOf("😀z", length))}, "page", object("sourceVersion", "physical-v1", "historyComplete", true))))
		rows = append(rows, r)
	}
	for _, action := range []string{"list", "read", "create", "send", "wait", "unknown"} {
		for _, body := range []any{nil, []any{}, "primitive", object()} {
			if action == "wait" {
				if _, ok := body.(protocol.JSONObject); ok {
					body = object("afterRevision", -1)
				}
			}
			rows = append(rows, scenario(op("api", "route", action, "body", body)))
		}
		rows = append(rows, scenario(op("api", "route", action, "method", "GET", "body", object())))
	}
	for _, field := range []string{"query", "limit", "offset"} {
		for _, value := range []any{false, nil, "", -1, 1.5, []any{}} {
			rows = append(rows, scenario(op("api", "route", "list", "body", object(field, value))))
		}
	}
	for _, provider := range []any{nil, "", false, []any{}, object()} {
		rows = append(rows, scenario(op("api", "route", "create", "body", object("prompt", "from API", "providerId", provider))))
	}
	rows = append(rows,
		scenario(op("api", "route", "list", "body", object("query", "Main", "limit", 1, "offset", 0))),
		scenario(op("api", "route", "read", "body", object("sessionId", "existing", "includeOutputs", "true", "limit", 1))),
		scenario(op("api", "route", "send", "body", object("targetSessionId", "existing", "content", "message", "messageId", "stable"))),
		scenario(createOp("root", "task"), op("api", "route", "wait", "body", object("afterRevision", 0, "timeoutMs", 900000))),
		scenario(createOp("root", "task"), op("api", "route", "wait", "body", object("afterRevision", 0, "timeoutMs", 1e100))),
		scenario(op("api", "route", "list", "bodyText", "{invalid")),
		scenario(op("ui", "parts", []string{}, "query", "Main"), op("ui", "parts", []string{"root", "status"}), op("ui", "parts", []string{"root", "stop"}, "method", "POST"), op("ui", "parts", []string{"root", "resume"}, "method", "POST"), op("ui", "parts", []string{"unknown", "oops"})),
	)
	return rows
}

func apiResponse(result protocol.JSONObject, e error) protocol.JSONObject {
	status := 200
	if e != nil {
		status = 500
		result = object("error", "INTERNAL_ERROR", "message", "Internal server error")
		if a, ok := e.(*APIError); ok {
			status = a.Status
			result = object("error", a.Code)
			if a.Message != "" {
				result.Set("message", raw(a.Message))
			}
		}
	}
	return object("status", status, "body", result)
}
func stringsOf(s string, n int) string {
	var b bytes.Buffer
	for i := 0; i < n; i++ {
		b.WriteString(s)
	}
	return b.String()
}

// Compare JSON trees without replacing lone UTF-16 surrogates at a Go string
// decoding boundary. Object insertion order is relevant inside serialized
// strings, while ordinary returned object fields may be compared by key.
func exactTree(t *testing.T, data json.RawMessage) any {
	t.Helper()
	if len(data) > 0 && data[0] == '{' {
		o := parsed(data)
		out := map[string]any{}
		for _, f := range o.OwnProperties() {
			key, e := protocol.ParseAndStringifyJavaScriptJSON(f.NameJSON)
			if e != nil {
				t.Fatal(e)
			}
			out[string(key)] = exactTree(t, f.Value)
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
			out = append(out, exactTree(t, v))
		}
		return out
	}
	normalized, e := protocol.ParseAndStringifyJavaScriptJSON(data)
	if e != nil {
		t.Fatal(e)
	}
	return string(normalized)
}

func runGoScenario(t *testing.T, row protocol.JSONObject) protocol.JSONObject {
	t.Helper()
	kind := row.String("kind")
	if kind == "migration" {
		r, e := Migrate(parsed(row.Get("value")))
		return resultObservation(r, e)
	}
	if kind == "projection" {
		return resultObservation(ProjectHistory(parsed(row.Get("value")), parsed(row.Get("options"))), nil)
	}
	root := t.TempDir()
	path := filepath.Join(root, "state.json")
	if initial := row.Get("initial"); initial != nil {
		if e := os.WriteFile(path, initial, 0600); e != nil {
			t.Fatal(e)
		}
	}
	started, injected := []protocol.JSONObject{}, []protocol.JSONObject{}
	stopped := []string{}
	reads := []protocol.JSONObject{}
	child, ordinal := 0, 0
	o := Options{StatePath: path, Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 6*int(time.Millisecond), time.UTC) }, UUID: func() string { ordinal++; return fmt.Sprintf("00000000-0000-4000-8000-%012d", ordinal) }, List: func(context.Context, protocol.JSONObject) (protocol.JSONObject, error) {
		if r := parsed(row.Get("sessionList")); r.IsObject() {
			return r, nil
		}
		return object("sessions", []protocol.JSONObject{object("id", "root", "title", "Main", "workDir", "/fixture", "modifiedAt", "now")}), nil
	}, Read: func(_ context.Context, id string, options protocol.JSONObject) (protocol.JSONObject, error) {
		reads = append(reads, object("sessionId", id, "options", options))
		if r := parsed(row.Get("history")); r.IsObject() {
			return r, nil
		}
		return object("messages", []protocol.JSONObject{}, "page", object("historyComplete", true)), nil
	}, Exists: func(_ context.Context, id string) (bool, error) { return id != "missing", nil }, Create: func(context.Context, string, protocol.JSONObject) (protocol.JSONObject, error) {
		if message := row.String("createFailure"); message != "" {
			return protocol.JSONObject{}, fmt.Errorf("%s", message)
		}
		child++
		return object("sessionId", fmt.Sprintf("child-%d", child), "workDir", "/fixture/worktree"), nil
	}, Start: func(_ context.Context, _ string, m protocol.JSONObject) error {
		started = append(started, clone(m))
		if message := row.String("startFailure"); message != "" {
			return fmt.Errorf("%s", message)
		}
		return nil
	}, Enqueue: func(_ context.Context, _ string, m protocol.JSONObject) error {
		injected = append(injected, clone(m))
		if message := row.String("enqueueFailure"); message != "" {
			return fmt.Errorf("%s", message)
		}
		return nil
	}, Stop: func(_ context.Context, id string) error {
		stopped = append(stopped, id)
		if message := row.String("stopFailure"); message != "" {
			return fmt.Errorf("%s", message)
		}
		return nil
	}}
	if row.Get("live") != nil {
		o.RuntimeState = func(id string) string { return parsed(row.Get("live")).String(id) }
	}
	o.BeginStop = func(ctx context.Context, id string) <-chan error {
		result := make(chan error, 1)
		result <- o.Stop(ctx, id)
		close(result)
		return result
	}
	if row.Get("titles") != nil {
		o.Titles = func(context.Context, []string) (protocol.JSONObject, error) { return parsed(row.Get("titles")), nil }
	}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	defer func() { _ = s.Close(ctx) }()
	observations := []protocol.JSONObject{}
	var lastCursor json.RawMessage
	for _, op := range list(row.Get("operations")) {
		var result any
		var e error
		caller := op.String("caller")
		if caller == "" {
			caller = "root"
		}
		switch op.String("action") {
		case "api":
			method := op.String("method")
			if method == "" {
				method = "POST"
			}
			body := string(op.Get("body"))
			if op.Get("bodyText") != nil {
				body = op.String("bodyText")
			}
			request := httptest.NewRequest(method, "http://127.0.0.1/api/session-collaboration/"+op.String("route"), strings.NewReader(body))
			value, failure := s.HandleAction(request, op.String("route"), caller)
			result = apiResponse(value, failure)
		case "ui":
			method := op.String("method")
			if method == "" {
				method = "GET"
			}
			var parts []string
			_ = decode(op.Get("parts"), &parts)
			value, failure := s.HandleUI(ctx, method, parts, op.Get("query"))
			result = apiResponse(value, failure)
		case "create":
			result, e = s.Create(ctx, caller, parsed(op.Get("input")))
		case "send":
			input := object("content", op.Get("content"))
			copyRaw(&input, "messageId", op.Get("messageId"))
			result, e = s.Send(ctx, caller, op.String("target"), input)
		case "status":
			var names []string
			_ = decode(op.Get("ids"), &names)
			result, e = s.Status(ctx, names)
		case "group":
			result, e = s.GroupStatus(ctx, op.String("id"))
		case "candidates":
			result, e = s.Candidates(ctx, op.Get("query"))
		case "state":
			value := object()
			copyRaw(&value, "result", op.Get("result"))
			e = s.OnSessionState(ctx, op.String("id"), op.String("state"), value, op.String("eventId"))
		case "consumed":
			e = s.OnMessageConsumed(ctx, op.String("messageId"), op.String("target"))
		case "stop":
			e = s.Stop(ctx, op.String("id"))
		case "stopped":
			e = s.OnStopped(ctx, op.String("id"))
		case "stopGroup":
			e = s.StopGroup(ctx, op.String("id"))
		case "resume":
			e = s.Resume(ctx, op.String("id"))
		case "input":
			e = s.OnUserInput(ctx, op.String("id"), op.Get("dispatch") == nil || isTrue(op.Get("dispatch")))
		case "recover":
			e = s.Recover(ctx)
		case "restart":
			_ = s.Close(ctx)
			s, e = New(o)
			if e == nil {
				e = s.Recover(ctx)
			}
		case "historyCommit":
			row.Set("history", op.Get("history"))
		case "read":
			result, e = s.Read(ctx, op.String("id"), parsed(op.Get("options")))
			if e == nil {
				r := result.(protocol.JSONObject)
				lastCursor = parsed(r.Get("page")).Get("nextCursor")
			}
		case "readNext":
			options := parsed(op.Get("options"))
			if lastCursor != nil && string(lastCursor) != "null" {
				options.Set("cursor", lastCursor)
			}
			result, e = s.Read(ctx, op.String("id"), options)
			if e == nil {
				r := result.(protocol.JSONObject)
				lastCursor = parsed(r.Get("page")).Get("nextCursor")
			}
		case "wait":
			var names []string
			_ = decode(op.Get("ids"), &names)
			timeout := WaitDefaultMS
			if op.Get("timeout") != nil {
				timeout = int(num(op.Get("timeout")))
			}
			result, e = s.Wait(ctx, num(op.Get("after")), names, timeout, op.String("caller"))
		case "canDeliver":
			result = s.CanDeliver(op.String("messageId"))
		default:
			t.Fatal("unknown operation", op.String("action"))
		}
		observations = append(observations, resultObservation(result, e))
	}
	snapshot, e := s.Status(ctx, nil)
	var state any = snapshot
	if e != nil {
		state = errorObservation(e)
	}
	var persisted any
	data, e := os.ReadFile(path)
	if e == nil {
		var value protocol.JSONObject
		_ = value.UnmarshalJSON(data)
		persisted = value
	} else if !os.IsNotExist(e) {
		t.Fatal(e)
	}
	return object("observations", observations, "snapshot", state, "persisted", persisted, "started", started, "injected", injected, "stopped", stopped, "reads", reads)
}
func TestCollaborationStateLifecycleAndHistoryMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config, temp := filepath.Join(home, ".claude"), filepath.Join(home, "tmp")
	for _, p := range []string{config, temp} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-collaboration.fixture.ts")
	rows := sourceCases()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_COLLABORATION_FIXTURE=1", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(object("homeDir", home, "cases", rows)))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source failed: %v %.2800s", e, stderr.String())
	}
	var source struct{ Results []json.RawMessage }
	if e = json.Unmarshal(data, &source); e != nil || len(source.Results) != len(rows) {
		t.Fatalf("Source shape %v %.2800s", e, data)
	}
	for i, row := range rows {
		t.Run(fmt.Sprintf("%03d-%s", i, row.String("kind")), func(t *testing.T) {
			got := runGoScenario(t, row)
			actual, expected := exactTree(t, raw(got)), exactTree(t, source.Results[i])
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("Source differs case %d\nGo: %s\nSource: %s", i, raw(got), source.Results[i])
			}
		})
	}
}
