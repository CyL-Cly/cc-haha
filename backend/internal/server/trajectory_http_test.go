package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

func TestPhysicalTrajectoryHTTPUsesActualTranscriptSnapshotScopeAndTraceLocators(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Error("read-only trajectory opened model runtime")
		return nil, errors.New("unexpected runtime")
	})
	directory := filepath.Join(options.ConfigDir, "projects", projectpath.Sanitize(options.WorkDir))
	sid := "12708de3-1d5a-48c1-85d7-a1522cf897bd"
	os.MkdirAll(directory, 0700)
	user := fmt.Sprintf(`{"type":"user","uuid":"u","timestamp":"2026-10-12T00:00:01.000Z","cwd":%q,"sessionId":%q,"message":{"role":"user","content":"physical fixture"}}`, options.WorkDir, sid)
	os.WriteFile(filepath.Join(directory, sid+".jsonl"), []byte(user+"\n"), 0600)
	sessionDir := filepath.Join(directory, sid)
	os.MkdirAll(filepath.Join(sessionDir, "subagents"), 0700)
	child := `{"type":"user","isSidechain":true,"uuid":"child","timestamp":"2026-10-12T00:00:02Z","message":{"role":"user","content":"child fixture"}}`
	os.WriteFile(filepath.Join(sessionDir, "subagents", "agent-child.jsonl"), []byte(child+"\n"), 0600)
	snapshot := `{"t":"blob","k":"sys","h":"1111111111111111","text":"actual snapshot"}` + "\n" + `{"t":"req","scope":"main","sys":"1111111111111111","tools":"2222222222222222","qs":"sdk","ts":"2026-10-12T00:00:00Z"}` + "\n"
	os.WriteFile(filepath.Join(sessionDir, "prompt-snapshots.jsonl"), []byte(snapshot), 0600)
	trace, e := tracecapture.New(tracecapture.Options{Scope: options.ConfigDir, ResolveMode: func() string { return "off" }})
	if e != nil {
		t.Fatal(e)
	}
	defer trace.Close(context.Background())
	options.Trace = trace
	traceDir := filepath.Join(options.ConfigDir, "cc-haha", "traces")
	os.MkdirAll(traceDir, 0700)
	call := `{"id":"physical-call","sessionId":"` + sid + `","source":"anthropic","startedAt":"2026-10-12T00:00:02.000Z","request":{"body":{"preview":"physical-body"}}}`
	os.WriteFile(filepath.Join(traceDir, sid+".jsonl"), []byte(call+"\n"), 0600)
	_, host := testHost(t, options)
	base := "/api/sessions/" + sid
	page := requestJSON(t, host, "GET", base+"/trajectory", "", 200)
	var parsed map[string]any
	json.Unmarshal(page, &parsed)
	if len(parsed["rows"].([]any)) != 1 || len(parsed["snapshots"].([]any)) != 1 {
		t.Fatal(string(page))
	}
	childPage := requestJSON(t, host, "GET", base+"/trajectory?agentId=agent-child", "", 200)
	json.Unmarshal(childPage, &parsed)
	if len(parsed["rows"].([]any)) != 1 || len(parsed["snapshots"].([]any)) != 0 {
		t.Fatal(string(childPage))
	}
	requestJSON(t, host, "GET", base+"/trajectory/rows/u:u?loc=0-"+fmt.Sprint(len(user)), "", 200)
	requestJSON(t, host, "GET", base+"/trajectory/rows/u:wrong?loc=0-"+fmt.Sprint(len(user)), "", 409)
	requestJSON(t, host, "GET", base+"/trajectory?agentId=../escape", "", 400)
	requestJSON(t, host, "GET", base+"/trajectory/snapshots/1111111111111111", "", 200)
	requestJSON(t, host, "POST", base+"/trajectory", "", 405)
	near := requestJSON(t, host, "GET", base+"/trace/near?from=2026-10-12T00%3A00%3A00Z&to=2026-10-12T00%3A01%3A00Z", "", 200)
	json.Unmarshal(near, &parsed)
	if len(parsed["calls"].([]any)) != 1 || parsed["locs"].(map[string]any)["physical-call"] == nil {
		t.Fatal(string(near))
	}
	loc := parsed["locs"].(map[string]any)["physical-call"].(string)
	requestJSON(t, host, "GET", base+"/trace/calls/physical-call?at="+loc, "", 200)
	requestJSON(t, host, "GET", base+"/trace/near?from=invalid&to=invalid", "", 400)
	requestJSON(t, host, "GET", base+"/trace/calls/physical-call?at=not-a-range", "", 400)
	response, e := host.Client().Get(host.URL + base + "/trajectory?agentId=missing")
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.StatusCode)
	}
}

func TestTraceNearAndLocatorHTTPAgainstActualMainSource(t *testing.T) {
	root := t.TempDir()
	sourceHome, goHome := filepath.Join(root, "source"), filepath.Join(root, "go")
	config := filepath.Join(goHome, ".claude")
	call := `{"id":"call","sessionId":"s","source":"anthropic","startedAt":"2026-10-12T00:00:02.000Z","request":{"body":{"preview":"body","bytes":4}}}`
	files := map[string]string{"cc-haha/traces/s.jsonl": call + "\n"}
	rows := []map[string]any{{"path": "/api/sessions/s/trace/near?from=2026-10-12T00%3A00%3A00Z&to=2026-10-12T00%3A01%3A00Z"}, {"path": "/api/sessions/s/trace/near?from=2026-10-12T00%3A00%3A03Z&to=2026-10-12T00%3A00%3A02Z"}, {"path": "/api/sessions/s/trace/near?from=2026-10-12T00%3A00%3A00Z&to=2026-10-12T02%3A00%3A00Z"}, {"path": "/api/sessions/s/trace/near?from=invalid&to=invalid"}, {"path": "/api/sessions/missing/trace/near?from=2026-10-12T00%3A00%3A00Z&to=2026-10-12T00%3A01%3A00Z"}, {"path": "/api/sessions/s/trace/calls/call?at=0-" + fmt.Sprint(len(call)+1)}, {"path": "/api/sessions/s/trace/calls/call?at=bad"}}
	expected := traceFixture(t, sourceHome, map[string]any{"action": "http", "mode": "off", "files": files, "mtime": "2026-10-12T00:00:00.000Z", "requests": rows})
	for path, value := range files {
		target := filepath.Join(config, path)
		os.MkdirAll(filepath.Dir(target), 0700)
		if e := os.WriteFile(target, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	trace, e := tracecapture.New(tracecapture.Options{Scope: config, ResolveMode: func() string { return "off" }})
	if e != nil {
		t.Fatal(e)
	}
	defer trace.Close(context.Background())
	_, host := testHost(t, Options{HomeDir: goHome, ConfigDir: config, WorkDir: goHome, Trace: trace, Factory: func(context.Context, SessionOptions, Emit) (LiveSession, error) { return nil, errors.New("read-only") }})
	for n, row := range rows {
		response, e := host.Client().Get(host.URL + row["path"].(string))
		if e != nil {
			t.Fatal(e)
		}
		var body any
		json.NewDecoder(response.Body).Decode(&body)
		response.Body.Close()
		actual := map[string]any{"status": response.StatusCode, "body": body}
		if !reflect.DeepEqual(reduceTraceHTTP(actual, goHome), reduceTraceHTTP(expected[n], sourceHome)) {
			t.Fatalf("case %d Go %v Source %v", n, actual, expected[n])
		}
	}
}
