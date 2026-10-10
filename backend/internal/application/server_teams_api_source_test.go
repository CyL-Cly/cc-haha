package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestProductionDesktopTeamsHTTPRoutesAndOriginalErrorsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	files := []map[string]string{{"path": ".claude/settings.json", "content": `{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false,"alwaysThinkingEnabled":false}`}, {"path": ".claude/teams/team/config.json", "content": `{"name":"team","createdAt":1000,"leadAgentId":"team-lead@team","leadSessionId":"aaaaaaaa-aaaa-4aaa-8aaa-000000000001","members":[{"agentId":"team-lead@team","name":"team-lead","cwd":"/actual/source","isActive":false},{"agentId":"worker@team","name":"worker","cwd":"/actual/source","isActive":false}]}`}, {"path": ".claude/projects/p/aaaaaaaa-aaaa-4aaa-8aaa-000000000001/subagents/agent-worker.jsonl", "content": `{"type":"assistant","uuid":"a","agentName":"worker","timestamp":"1970-01-01T00:00:01.000Z","message":{"content":"Actual worker transcript"}}` + "\n"}}
	input := []protocol.JSONObject{}
	add := func(method, path string, body *string) {
		row := protocol.NewJSONObject(field("method", method), field("path", path))
		if body != nil {
			row.Set("body", raw(*body))
		}
		input = append(input, row)
	}
	for _, path := range []string{"/api/teams", "/api/teams/team", "/api/teams/team/unknown-suffix", "/api/teams/missing", "/api/teams/team/workbench", "/api/teams/team/members/worker%40team/transcript", "/api/teams/team/members/missing/transcript", "/api/teams/session/aaaaaaaa-aaaa-4aaa-8aaa-000000000001/plan", "/api/teams/session/aaaaaaaa-aaaa-4aaa-8aaa-000000000099/workbench?at=0x100000000000000000000", "/api/teams/team/members/worker%40team/transcript?incremental=TRUE", "/api/teams/team/plan/not-action"} {
		add("GET", path, nil)
	}
	for _, body := range []string{"bad JSON", "null", "{}", `{"content":"  "}`, `{"content":false}`, `{"content":"Actual user message"}`} {
		value := body
		add("POST", "/api/teams/team/members/worker%40team/messages", &value)
	}
	for _, method := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		add(method, "/api/teams", nil)
	}
	for _, body := range []string{"bad JSON", "null", "{}", `{"planId":"p","sessionId":"s","incarnationId":"i","expectedRevision":0,"requestId":"r"}`} {
		value := body
		add("POST", "/api/teams/team/plan/unknown-action", &value)
	}
	for _, body := range []string{"bad JSON", "null", "{}"} {
		value := body
		add("PATCH", "/api/teams/team/plan", &value)
	}
	add("DELETE", "/api/teams/team", nil)
	sourceHome := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-teams-api.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_TEAMS_API_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": sourceHome, "files": files, "rows": input, "now": 20000}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Observations []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Observations) != len(input) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home := o.Environment["HOME"]
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	for _, file := range files {
		path := filepath.Join(home, file["path"])
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		if err = os.WriteFile(path, []byte(file["content"]), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, nil }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		if err == nil {
			native.Now = func() time.Time { return time.UnixMilli(20000) }
			native.NowMillis = func() float64 { return 20000 }
		}
		return native, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	for i, row := range input {
		var body io.Reader
		if row.Get("body") != nil {
			body = bytes.NewBufferString(row.String("body"))
		}
		request, err := http.NewRequest(row.String("method"), host.URL+row.String("path"), body)
		if err != nil {
			t.Fatal(err)
		}
		response, err := host.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		actual := raw(map[string]any{"status": response.StatusCode, "body": obj(data)})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Observations[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d %s %s Go %s Source %s", i, row.String("method"), row.String("path"), actual, source.Observations[i])
		}
	}
	data, err := os.ReadFile(filepath.Join(config, "teams", "team", "inboxes", "worker.json"))
	if err != nil || !bytes.Contains(data, []byte("Actual user message")) {
		t.Fatal("HTTP did not deliver to actual mailbox", err, string(data))
	}
}
