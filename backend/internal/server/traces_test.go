package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

func traceTestJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func traceFixture(t *testing.T, home string, input any) []any {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source Bun unavailable")
	}
	if e := os.MkdirAll(filepath.Join(home, "tmp"), 0700); e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-trace.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", "run", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "NODE_ENV=test", "USER_TYPE=external", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TRACE_FIXTURE=1", "TZ=UTC"}
	cmd.Stdin = bytes.NewReader(traceTestJSON(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, e := cmd.Output()
	if e != nil {
		t.Fatalf("Actual Source HTTP %v %s", e, stderr.String())
	}
	var out struct{ Observations []any }
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatalf("Actual Source JSON %v %s", e, b)
	}
	return out.Observations
}
func reduceTraceHTTP(v any, home string) any {
	var out any
	_ = json.Unmarshal(traceTestJSON(v), &out)
	var visit func(any) any
	visit = func(v any) any {
		switch x := v.(type) {
		case []any:
			for n := range x {
				x[n] = visit(x[n])
			}
		case map[string]any:
			for _, k := range []string{"revisionToken", "nextScanCursor", "fileUpdatedAt", "revision", "messageSignature"} {
				delete(x, k)
			}
			for k, v := range x {
				x[k] = visit(v)
			}
		case string:
			return strings.ReplaceAll(x, home, "$HOME")
		}
		return v
	}
	return visit(out)
}
func TestTraceHTTPAgainstActualSource(t *testing.T) {
	allocation := t.TempDir()
	sourceHome, goHome := filepath.Join(allocation, "source-home"), filepath.Join(allocation, "go-home")
	config := filepath.Join(goHome, ".claude")
	record := map[string]any{"id": "call1", "sessionId": "s", "source": "anthropic", "startedAt": "2026-10-01T00:00:00.000Z", "completedAt": "2026-10-01T00:00:01.000Z", "durationMs": 1000, "model": "model", "status": "ok", "request": map[string]any{"method": "POST", "url": "https://fixture.invalid/messages", "headers": map[string]any{}, "body": map[string]any{"contentType": "text", "preview": "request", "bytes": 7, "sha256": "fixture-hash", "truncated": false}}, "response": map[string]any{"status": 200, "headers": map[string]any{}, "body": map[string]any{"contentType": "json", "preview": "{\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}", "bytes": 47, "sha256": "fixture-res-hash", "truncated": false}}}
	canonical := string(traceTestJSON(map[string]any{"type": "call", "record": record})) + "\n"
	files := map[string]string{"cc-haha/traces/s.jsonl": canonical, "cc-haha/settings.json": "{\"traceCapture\":{\"enabled\":true,\"unknown\":\"keep\"},\"other\":{\"flag\":true}}"}
	rows := []map[string]any{{"path": "/api/traces/settings"}, {"method": "PUT", "path": "/api/traces/settings", "raw": "{\"enabled\":false}"}, {"path": "/api/traces/settings"}, {"method": "PUT", "path": "/api/traces/settings", "raw": "{\"enabled\":\"false\"}"}, {"method": "PUT", "path": "/api/traces/settings", "raw": "[]"}, {"method": "PUT", "path": "/api/traces/settings", "raw": "null"}, {"method": "PUT", "path": "/api/traces/settings", "raw": "{"}, {"method": "POST", "path": "/api/traces/settings"}, {"method": "POST", "path": "/api/traces"}, {"path": "/api/traces/unknown"}, {"path": "/api/traces"}, {"path": "/api/traces?q=s&limit=5junk&offset=0"}, {"path": "/api/traces?q=absent"}, {"path": "/api/traces?offset=99"}, {"path": "/api/traces/s/revision"}, {"path": "/api/traces/missing/revision?sinceRevision=0"}, {"path": "/api/traces/missing/revision?sinceRevision="}, {"path": "/api/traces/s/revision?sinceRevision=-1"}, {"path": "/api/traces/s/revision?sinceRevision=2.3"}, {"path": "/api/traces/s/revision?sinceRevision=Infinity"}, {"path": "/api/traces/s/revision?sinceRevision=9007199254740992"}, {"path": "/api/traces/s/revision?sinceRevisionToken="}, {"path": "/api/traces/%FF/revision"}, {"method": "POST", "path": "/api/traces/s/revision"}, {"path": "/api/sessions/s/trace"}, {"path": "/api/sessions/s/trace/raw"}, {"path": "/api/sessions/s/trace/calls/call1"}, {"path": "/api/sessions/s/trace/calls/none"}, {"path": "/api/sessions/s/trace/calls"}, {"path": "/api/sessions/s/trace?offset=-1"}, {"path": "/api/sessions/s/trace?offset="}, {"path": "/api/sessions/s/trace?offset=1junk"}, {"path": "/api/sessions/s/trace?revisionToken=stale"}, {"path": "/api/sessions/s/trace?scanCursor=invalid"}, {"method": "POST", "path": "/api/sessions/s/trace"}, {"path": "/api/sessions/missing/trace"}, {"path": "/api/sessions/missing/trace/raw"}, {"method": "DELETE", "path": "/api/traces/s"}, {"method": "DELETE", "path": "/api/traces/s"}, {"path": "/api/traces"}}
	expected := traceFixture(t, sourceHome, map[string]any{"action": "http", "mode": "off", "files": files, "mtime": "2026-10-01T00:00:00.000Z", "requests": rows})
	for p, data := range files {
		path := filepath.Join(config, p)
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
		stamp := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		_ = os.Chtimes(path, stamp, stamp)
	}
	service, e := tracecapture.New(tracecapture.Options{Scope: config, ResolveMode: func() string { return "off" }})
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close(context.Background())
	server, e := New(Options{HomeDir: goHome, ConfigDir: config, WorkDir: goHome, Trace: service, Factory: func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Error("Trace-only API unexpectedly opened model runtime")
		return nil, errors.New("Trace does not invoke model")
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close(context.Background())
	host := httptest.NewServer(server)
	defer host.Close()
	for n, row := range rows {
		method := "GET"
		if m, ok := row["method"].(string); ok {
			method = m
		}
		body, _ := row["raw"].(string)
		request, e := http.NewRequest(method, host.URL+row["path"].(string), strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		response, e := host.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		var value any
		if e = json.Unmarshal(b, &value); e != nil {
			value = string(b)
		}
		out := map[string]any{"status": response.StatusCode, "body": reduceTraceHTTP(value, goHome)}
		if strings.Contains(row["path"].(string), "/raw") && response.StatusCode == 200 {
			out["headers"] = map[string]any{"content-type": response.Header.Get("content-type"), "content-disposition": response.Header.Get("content-disposition"), "cache-control": response.Header.Get("cache-control")}
		}
		actual := reduceTraceHTTP(out, goHome)
		t.Run(strconvTrace(n), func(t *testing.T) {
			if !reflect.DeepEqual(actual, expected[n]) {
				t.Fatalf("Path %s Source=%s Native=%s", row["path"], traceTestJSON(expected[n]), traceTestJSON(actual))
			}
		})
	}
}
func strconvTrace(n int) string { b, _ := json.Marshal(n); return string(b) }
