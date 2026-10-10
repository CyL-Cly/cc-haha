package tracecapture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func fixtureHome(t *testing.T) string {
	t.Helper()
	h := filepath.Join(t.TempDir(), "home")
	if e := os.MkdirAll(filepath.Join(h, "tmp"), 0700); e != nil {
		t.Fatal(e)
	}
	return h
}
func source(t *testing.T, input any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source Bun unavailable")
	}
	h := fixtureHome(t)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-trace.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", "run", fixture)
	cmd.Dir = h
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + h, "USERPROFILE=" + h, "CLAUDE_CONFIG_DIR=" + filepath.Join(h, ".claude"), "TMPDIR=" + filepath.Join(h, "tmp"), "NODE_ENV=test", "USER_TYPE=external", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(h), "CC_HAHA_TRACE_FIXTURE=1", "TZ=UTC"}
	cmd.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, e := cmd.Output()
	if e != nil {
		t.Fatalf("Actual Source: %v %s", e, stderr.String())
	}
	var out protocol.JSONObject
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatalf("Source JSON: %v %s", e, b)
	}
	return out
}
func jsonComparable(v any) any { var out any; _ = json.Unmarshal(raw(v), &out); return out }
func compare(t *testing.T, source, native any) {
	t.Helper()
	if !reflect.DeepEqual(jsonComparable(source), jsonComparable(native)) {
		t.Fatalf("%s", firstDifference(jsonComparable(source), jsonComparable(native), "$"))
	}
}
func TestBodyAndSemanticAgainstActualSource(t *testing.T) {
	cases := []protocol.JSONObject{
		obj("body", nil), obj("body", ""), obj("body", "  {\"api_key\":\"fake-secret\",\"input_tokens\":12,\"output_tokens\":3,\"sessionId\":\"private\",\"text\":\"Bearer dummyFake+token\"}  "),
		obj("body", []any{1, "sk-dummyfaketoken12345", nil}), obj("body", json.RawMessage(`{"2":1,"1":2,"float":1e-7,"lone":"\ud800","text":"sk-abcdefgh-"}`)),
		obj("body", json.RawMessage(`"A😀B"`), "maxPreview", 2), obj("body", "text bearer\tfoo/bar== end", "alreadyTruncated", true),
		obj("body", obj("model", "model-id", "messages", []any{obj("role", "user", "content", []any{obj("type", "image", "source", obj("type", "base64", "media_type", "image/png", "data", "YWI=")), obj("type", "text", "text", "do it")})})),
		obj("body", obj("anthropic", obj("system", "system prompt", "messages", []any{}), "upstream", obj("secret", "fake")), "source", "proxy"),
		obj("body", " true "), obj("body", obj("instructions", "use tools", "input", []any{}, "access_token", "redact")),
	}
	deep := json.RawMessage(`"end"`)
	for j := 0; j < 34; j++ {
		deep = raw([]json.RawMessage{deep})
	}
	cases = append(cases, obj("body", deep))
	many := make([]int, 4097)
	cases = append(cases, obj("body", many))
	large := bytes.Repeat([]byte("a"), CaptureCharLimit+1)
	cases = append(cases, obj("body", string(large)))
	expected := array(source(t, obj("action", "value", "cases", cases)).Get("observations"))
	if len(expected) != len(cases) {
		t.Fatal("Source case coverage")
	}
	for n, row := range cases {
		maxPreview := PreviewChars
		if row.Get("maxPreview") != nil {
			maxPreview = int(number(row.Get("maxPreview")))
		}
		src := text(row.Get("source"))
		if src == "" {
			src = "anthropic"
		}
		sem := semantic(row.Get("body"), src)
		if sem == nil {
			sem = json.RawMessage("null")
		}
		t.Run(integerString(int64(n)), func(t *testing.T) {
			compare(t, expected[n], obj("body", CreateBodySnapshot(row.Get("body"), maxPreview, truthy(row.Get("alreadyTruncated"))), "semantic", sem))
		})
	}
}
func TestTraceSchemaOldFilesAgainstActualSource(t *testing.T) {
	ctx := context.Background()
	for v := 0; v <= 6; v++ {
		t.Run(integerString(int64(v)), func(t *testing.T) {
			seed := []string{}
			if v > 0 {
				seed = []string{`INSERT INTO trace_sources(session_id,file_path,size_bytes,mtime_ms,indexed_bytes,revision,last_reset_revision,state,last_error_code,updated_at_ms)VALUES('old','/fixture/old.jsonl',300,42,300,7,3,'ready',NULL,42)`, `INSERT INTO trace_calls(session_id,call_id,ordinal,byte_start,byte_length,revision,started_at,completed_at,status,source,model,duration_ms,failed,input_tokens,output_tokens)VALUES('old','old-call',2,0,300,7,'t1','t2','ok','anthropic','old-model',100,0,12,5)`}
			}
			if v >= 2 {
				seed = append(seed, `INSERT INTO trace_sessions(session_id,revision,last_reset_revision,next_ordinal,api_calls,failed_calls,total_duration_ms,total_input_tokens,total_output_tokens,summary_updated_at)VALUES('old',7,3,3,1,0,100,12,5,'t2')`, `INSERT INTO trace_session_models(session_id,model,call_count)VALUES('old','old-model',1)`)
			}
			if v >= 3 {
				seed = append(seed, `UPDATE trace_sessions SET reset_token='old-incarnation'`)
			}
			if v >= 4 {
				seed = append(seed, `UPDATE trace_calls SET first_ordinal=ordinal`, `UPDATE trace_session_models SET first_started_at='t1',first_ordinal=2`)
			}
			expected := source(t, obj("action", "schema", "oldVersion", v, "seed", seed))
			h := fixtureHome(t)
			path := filepath.Join(h, ".claude", "cc-haha", "db", "trace-index-v1.sqlite")
			if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				t.Fatal(e)
			}
			if v > 0 {
				db, e := localindex.OpenDatabase(ctx, localindex.DatabaseOptions{Path: path, Kind: localindex.Trace})
				if e != nil {
					t.Fatal(e)
				}
				e = db.Write(ctx, func(op *localindex.Operation) error {
					rows, e := op.All("SELECT name,type FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type DESC")
					if e != nil {
						return e
					}
					if e = op.Exec("PRAGMA foreign_keys=OFF"); e != nil {
						return e
					}
					for _, r := range rows {
						if e = op.Exec("DROP " + text(r.Get("type")) + " IF EXISTS " + text(r.Get("name"))); e != nil {
							return e
						}
					}
					schemas := object(expected.Get("schemas"))
					for n := 1; n <= v; n++ {
						if e = op.Exec(text(schemas.Get("SCHEMA_V" + integerString(int64(n))))); e != nil {
							return e
						}
					}
					for _, sql := range seed {
						if e = op.Exec(sql); e != nil {
							return e
						}
					}
					return op.Exec("PRAGMA user_version=" + integerString(int64(v)))
				})
				_ = db.Close()
				if e != nil {
					t.Fatal(e)
				}
			}
			db, e := localindex.OpenDatabase(ctx, localindex.DatabaseOptions{Path: path, Scope: filepath.Join(h, ".claude"), Kind: localindex.Trace})
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			e = db.Read(ctx, func(op *localindex.Operation) error {
				rows, e := op.All("SELECT type,name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name")
				if e != nil {
					return e
				}
				compare(t, expected.Get("schema"), rows)
				version, e := op.Get("PRAGMA user_version")
				if e != nil {
					return e
				}
				compare(t, expected.Get("version"), *version)
				size, e := op.Get("PRAGMA journal_size_limit")
				if e != nil {
					return e
				}
				compare(t, expected.Get("journalSize"), *size)
				for _, pair := range [][2]string{{"sources", "trace_sources"}, {"sessions", "trace_sessions"}, {"calls", "trace_calls"}, {"models", "trace_session_models"}} {
					rows, e := op.All("SELECT * FROM " + pair[1])
					if e != nil {
						return e
					}
					values := []protocol.JSONObject{}
					for _, row := range rows {
						row.Delete("reset_token")
						values = append(values, row)
					}
					expectedRows := []protocol.JSONObject{}
					for _, row := range array(expected.Get(pair[0])) {
						o := object(row)
						o.Delete("reset_token")
						expectedRows = append(expectedRows, o)
					}
					compare(t, expectedRows, values)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
func reduceObservations(v any) any {
	var decoded any
	_ = json.Unmarshal(raw(v), &decoded)
	var visit func(any) any
	visit = func(v any) any {
		switch x := v.(type) {
		case []any:
			for n := range x {
				x[n] = visit(x[n])
			}
		case map[string]any:
			for _, k := range []string{"resetToken", "revisionToken", "nextScanCursor", "fileUpdatedAt", "mtimeMs", "fileIdentity", "fingerprint", "revision", "lastResetRevision"} {
				delete(x, k)
			}
			for k, v := range x {
				x[k] = visit(v)
			}
		}
		return v
	}
	return visit(decoded)
}
func TestTraceIndexAgainstActualSource(t *testing.T) {
	ctx := context.Background()
	in := ProjectionInput{Source: SourceInput{SessionID: "s", FilePath: "/fixture/s.jsonl", Size: 300, MtimeMs: 123, IndexedBytes: 300}, Calls: []CallLocator{{ID: "c1", Ordinal: 0, ByteStart: 0, ByteLength: 100, StartedAt: "a", Status: "ok", Source: "anthropic", Model: ptr("m1"), DurationMs: ptr(10.0), InputTokens: 12, OutputTokens: 3, RequestBytes: ptr(23.0), ResponseBytes: ptr(35.0), ResponseStatus: ptr(200.0)}, {ID: "c2", Ordinal: 1, ByteStart: 100, ByteLength: 100, StartedAt: "a", CompletedAt: ptr("b"), Status: "error", Source: "proxy", Model: ptr("m2"), Failed: true}}, Events: []EventLocator{{ID: "e1", Ordinal: 2, ByteStart: 200, ByteLength: 100, Timestamp: "c", Phase: "api_call_completed", Severity: "info", CallID: ptr("c1"), Title: ptr("finished")}}}
	next := in
	next.Source.Size = 400
	next.Source.IndexedBytes = 400
	next.Calls = []CallLocator{in.Calls[0]}
	next.Calls[0].Ordinal = 3
	next.Calls[0].ByteStart = 300
	next.Calls[0].Status = "error"
	next.Calls[0].Failed = true
	next.Calls[0].Model = ptr("m2")
	next.Calls[0].OutputTokens = 7
	next.Events = []EventLocator{}
	operations := []protocol.JSONObject{obj("method", "replace", "input", in), obj("method", "get", "id", "s"), obj("method", "append", "input", next), obj("method", "get", "id", "s"), obj("method", "page", "id", "s", "offset", 1, "limit", 1), obj("method", "changes", "id", "s", "since", 1), obj("method", "changes", "id", "s", "since", 0), obj("method", "call", "id", "s", "callId", "c1"), obj("method", "list", "options", obj("limit", 50)), obj("method", "degrade", "id", "s", "code", "TRACE_INDEX_SYNC_FAILED"), obj("method", "delete", "id", "s"), obj("method", "get", "id", "s")}
	bad := in
	bad.Calls = append([]CallLocator(nil), in.Calls...)
	bad.Calls[0].Ordinal = 400
	operations = append(operations, obj("method", "replace", "input", bad))
	bad = in
	bad.Source.PendingTailBytes = ptr(int64(1))
	operations = append(operations, obj("method", "replace", "input", bad))
	bad = in
	bad.Calls = append([]CallLocator(nil), in.Calls...)
	bad.Calls[0].FirstOrdinal = ptr(int64(4))
	operations = append(operations, obj("method", "replace", "input", bad))
	expected := array(source(t, obj("action", "index", "operations", operations)).Get("observations"))
	db, e := localindex.OpenDatabase(ctx, localindex.DatabaseOptions{Path: filepath.Join(t.TempDir(), "trace.sqlite"), Kind: localindex.Trace})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	i := NewIndex(db)
	for n, row := range operations {
		var v any
		var e error
		switch text(row.Get("method")) {
		case "replace", "append":
			var input ProjectionInput
			_ = json.Unmarshal(row.Get("input"), &input)
			if text(row.Get("method")) == "replace" {
				v, e = i.Replace(ctx, input)
			} else {
				v, e = i.Append(ctx, input)
			}
		case "get":
			v, _, e = i.GetSession(ctx, text(row.Get("id")), nil, nil)
		case "page":
			p, total, err := i.GetSession(ctx, "s", ptr(int64(1)), ptr(int64(1)))
			e = err
			o := object(raw(p))
			o.Set("totalCalls", raw(p.Summary.APICalls))
			o.Set("totalEvents", raw(total))
			v = o
		case "changes":
			out, err := i.GetChanges(ctx, "s", int64(number(row.Get("since"))))
			e = err
			if out.IsObject() {
				v = out
			}
		case "call":
			s, c, err := i.GetCall(ctx, "s", "c1")
			e = err
			if c != nil {
				v = obj("source", s, "call", c)
			}
		case "list":
			sessions, total, err := i.List(ctx, nil, 0, 50)
			e = err
			v = obj("sessions", sessions, "total", total)
		case "degrade":
			e = i.Degrade(ctx, "s", "TRACE_INDEX_SYNC_FAILED")
			if e == nil {
				v, e = i.GetSource(ctx, "s")
			}
		case "delete":
			e = i.Delete(ctx, "s")
			if e == nil {
				v, e = i.GetSource(ctx, "s")
			}
		}
		out := obj("value", v)
		if e != nil {
			out = obj("error", e.Error())
		}
		t.Run(integerString(int64(n)), func(t *testing.T) { compare(t, reduceObservations(expected[n]), reduceObservations(out)) })
	}
}

func firstDifference(a, b any, path string) string {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return path + ": type differs"
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !reflect.DeepEqual(x[k], y[k]) {
				return firstDifference(x[k], y[k], path+"."+k)
			}
		}
		for k := range y {
			if _, ok := x[k]; !ok {
				return path + "." + k + ": unexpected native field"
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return fmt.Sprintf("%s: Source length %d Native %v", path, len(x), b)
		}
		for n := range x {
			if !reflect.DeepEqual(x[n], y[n]) {
				return firstDifference(x[n], y[n], fmt.Sprintf("%s[%d]", path, n))
			}
		}
	}
	return fmt.Sprintf("%s: Source=%v Native=%v", path, a, b)
}
