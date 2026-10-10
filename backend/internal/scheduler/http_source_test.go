package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func disposableOptions(t *testing.T) Options {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	return Options{Home: home, ConfigDir: config, Environment: func() map[string]string { return map[string]string{"CC_HAHA_LOCAL_INDEX": "off"} }, Now: time.Now, RandomBytes: func(n int) ([]byte, error) { return make([]byte, n), nil }, Files: OSFiles{}, Execute: func(context.Context, ExecutionRequest, func(json.RawMessage)) (ExecutionResult, error) {
		return ExecutionResult{}, errors.New("unconfigured test executor")
	}, CreateSession: func(context.Context, string, string) (string, error) {
		return "", errors.New("unconfigured test session")
	}, DeletePlaceholder: func(context.Context, string) error { return nil }, AppendSessionMetadata: func(context.Context, string, protocol.JSONObject) error { return nil }, GetProcessEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }, ProviderRuntimeEnv: func(context.Context, string) (map[string]string, error) { return nil, nil }, NetworkEnvironment: func(context.Context, map[string]string) (map[string]string, error) { return map[string]string{}, nil }, AgentTeamsEnabled: func(context.Context) (bool, error) { return false, nil }, OfficialOAuthEnv: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }, LogError: func(error) {}}
}
func TestSchedulerHTTPActualSourceAllReadCRUDAndQueryErrorContracts(t *testing.T) {
	actions := []map[string]any{
		{"method": "GET", "path": "/api/scheduled-tasks"},
		{"method": "POST", "path": "/api/scheduled-tasks", "body": `{"prompt":"actual task","cron":"* * * * *","name":"name","enabled":false,"permissionMode":"plan","futureDiscarded":1,"providerId":null}`},
		{"method": "GET", "path": "/api/scheduled-tasks"},
		{"method": "PUT", "path": "/api/scheduled-tasks/01010101", "body": `{"id":"wrong","createdAt":99,"enabled":true,"recurring":true,"permanent":true,"futureKept":1,"notification":{"enabled":true,"channels":["desktop"]}}`},
		{"method": "GET", "path": "/api/scheduled-tasks/runs"},
		{"method": "GET", "path": "/api/scheduled-tasks/old/runs"},
		{"method": "GET", "path": "/api/scheduled-tasks/runs?limit=1&summaryOnly=true"},
		{"method": "GET", "path": "/api/scheduled-tasks/runs?limit=foo"},
		{"method": "GET", "path": "/api/scheduled-tasks/runs?completedAfterMs=0x10"},
		{"method": "GET", "path": "/api/scheduled-tasks/runs?completedAfterMs=-1"},
		{"method": "GET", "path": "/api/scheduled-tasks/runs?completedAfterMs=nope"},
		{"method": "GET", "path": "/api/scheduled-tasks/runs?cursor=invalid&nonterminalOnly=true"},
		{"method": "GET", "path": "/api/scheduled-tasks/old/runs?limit=-2&summaryOnly=true"},
		{"method": "GET", "path": "/api/scheduled-tasks/runs/a"},
		{"method": "GET", "path": "/api/scheduled-tasks/runs/missing"},
		{"method": "POST", "path": "/api/scheduled-tasks/missing/run"},
		{"method": "POST", "path": "/api/scheduled-tasks", "body": "invalid"},
		{"method": "POST", "path": "/api/scheduled-tasks", "body": "{}"},
		{"method": "POST", "path": "/api/scheduled-tasks", "body": "null"},
		{"method": "POST", "path": "/api/scheduled-tasks", "body": "42"},
		{"method": "POST", "path": "/api/scheduled-tasks", "body": "[]"},
		{"method": "PUT", "path": "/api/scheduled-tasks/01010101", "body": "[1,2]"},
		{"method": "PUT", "path": "/api/scheduled-tasks/01010101", "body": `"x\ud800"`},
		{"method": "PUT", "path": "/api/scheduled-tasks/01010101", "body": "false"},
		{"method": "PATCH", "path": "/api/scheduled-tasks/old/runs"},
		{"method": "DELETE", "path": "/api/scheduled-tasks/01010101"},
		{"method": "DELETE", "path": "/api/scheduled-tasks/01010101"},
	}
	log := `{"unknownLogRoot":1,"runs":[{"id":"a","taskId":"old","startedAt":"2026-10-07T01:00:00Z","status":"completed","completedAt":"2026-10-07T02:00:00Z","output":"private result","prompt":"private prompt"},{"id":"b","taskId":"other","startedAt":"2026-10-06T01:00:00Z","status":"running"}]}`
	data := actualSource(t, "http", map[string]any{"actions": actions, "now": 1700000000000, "log": log})
	var source struct {
		Rows []struct {
			Status int
			Body   json.RawMessage
		}
	}
	if e := json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	o := disposableOptions(t)
	o.Now = func() time.Time { return time.UnixMilli(1700000000000) }
	random := 0
	o.RandomBytes = func(n int) ([]byte, error) {
		random++
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(random)
		}
		return b, nil
	}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop(context.Background())
	_ = os.WriteFile(s.store.runsPath(), []byte(log), 0600)
	for n, action := range actions {
		u, _ := url.Parse("http://fixture.invalid" + action["path"].(string))
		var body []byte
		if v := action["body"]; v != nil {
			body = []byte(v.(string))
		}
		status, result, e := s.Handle(context.Background(), action["method"].(string), u, strings.Split(strings.Trim(u.Path, "/"), "/"), body)
		if e != nil {
			var own *Error
			if !errors.As(e, &own) {
				own = &Error{500, "INTERNAL_ERROR", "An unexpected error occurred"}
			}
			status = own.Status
			result = protocol.NewJSONObject(protocol.JSONField{Name: "error", Value: quoted(own.Code)}, protocol.JSONField{Name: "message", Value: quoted(own.Message)})
		}
		if status != source.Rows[n].Status || !equalJSON(raw(result), source.Rows[n].Body) {
			t.Errorf("HTTP%d Go%d %s Source%d %s error%v", n, status, raw(result), source.Rows[n].Status, source.Rows[n].Body, e)
		}
	}
}
