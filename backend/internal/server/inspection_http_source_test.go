package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/slashcommandsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// A controlled CLI control transport supplies responses. The actual API,
// semantic transcript snapshot, compiled/disk commands and SDK capture run.
type inspectionTransportSession struct {
	mu        sync.Mutex
	responses map[string]any
	sent      []protocol.JSONObject
}

func (*inspectionTransportSession) Handle(context.Context, json.RawMessage) error { return nil }
func (*inspectionTransportSession) Snapshot(context.Context) ([]json.RawMessage, error) {
	return nil, nil
}
func (*inspectionTransportSession) Close(context.Context) error { return nil }
func (s *inspectionTransportSession) RequestInspectionControl(_ context.Context, input protocol.JSONObject, _ time.Duration) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, input.Clone())
	response := s.responses[input.String("subtype")].(map[string]any)
	if text, _ := response["error"].(string); text != "" {
		return nil, errors.New(text)
	}
	return recoveryJSON(response["value"]), nil
}

func TestInspectionHTTPModesActualSourceAndExistingSDKControlTransport(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun unavailable")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	id := "11111111-1111-4111-8111-111111111111"
	content := `{"type":"session-meta","workDir":"$HOME","permissionMode":"plan","runtimeProviderId":null}` + "\n" + `{"type":"assistant","uuid":"a1","timestamp":"2026-10-07T00:00:00Z","message":{"id":"api1","model":"deepseek-flash","role":"assistant","content":"physical response","usage":{"input_tokens":12,"output_tokens":8}}}` + "\n"
	init := map[string]any{"type": "system", "subtype": "init", "model": "live-model", "cwd": "$HOME", "claude_code_version": "actual-sdk-version", "apiKeySource": "env", "tools": []string{"Read"}, "mcp_servers": []any{}, "slash_commands": []string{"cost", "actual"}, "skills": []string{"physical"}}
	responses := map[string]any{"get_session_usage": map[string]any{"value": map[string]any{"totalInputTokens": 5, "totalOutputTokens": 6, "models": []any{}}}, "get_context_usage": map[string]any{"value": map[string]any{"actualContext": true}}, "mcp_status": map[string]any{"value": map[string]any{"mcpServers": []any{map[string]any{"name": "actual-peer", "status": "connected"}}}}}
	httpCases := []map[string]any{{"id": id}, {"id": id, "query": "?includeContext=0"}, {"id": id, "query": "?includeContext=0&usageOnly=1"}, {"id": id, "query": "?contextOnly=1"}, {"id": id, "method": "POST"}, {"id": "22222222-2222-4222-8222-222222222222"}, {"id": "22222222-2222-4222-8222-222222222222", "method": "POST"}}
	for _, query := range []string{"", "?includeContext=0", "?includeContext=0&usageOnly=1", "?contextOnly=1"} {
		httpCases = append(httpCases, map[string]any{"id": id, "active": true, "init": init, "query": query, "responses": responses})
	}
	failed := map[string]any{"get_session_usage": map[string]any{"error": "private usage failure"}, "get_context_usage": map[string]any{"error": "private context failure"}, "mcp_status": map[string]any{"error": "private MCP failure"}}
	for _, query := range []string{"", "?includeContext=0&usageOnly=1", "?contextOnly=1"} {
		httpCases = append(httpCases, map[string]any{"id": id, "active": true, "init": init, "query": query, "responses": failed})
	}
	arrayResponses := map[string]any{"get_session_usage": map[string]any{"value": []any{1, 2}}, "get_context_usage": map[string]any{"value": []any{}}, "mcp_status": map[string]any{"value": map[string]any{"mcpServers": []any{}}}}
	for _, query := range []string{"?includeContext=0&usageOnly=1", "?contextOnly=1"} {
		httpCases = append(httpCases, map[string]any{"id": id, "active": true, "init": init, "query": query, "responses": arrayResponses})
	}
	httpCases = append(httpCases, map[string]any{"id": id, "active": true, "query": "?includeContext=0", "responses": responses})
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "ANTHROPIC_API_KEY": "fake-no-network", "ANTHROPIC_BASE_URL": "http://127.0.0.1:43210", "ANTHROPIC_MODEL": "claude-sonnet-4-6", "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"deepseek-flash":1000000}`, "PATH": "/usr/bin:/bin"}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-inspection.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SESSION_INSPECTION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "TZ=UTC", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "environment": env, "cases": []any{map[string]any{"id": id, "name": "http-physical", "content": content}}, "httpCases": httpCases})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		HTTPResults []struct {
			Status int
			Body   json.RawMessage
			Sent   []protocol.JSONObject
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.HTTPResults) != len(httpCases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Invalid actual Source HTTP", err, source.Capabilities.Blocked, string(output)[:min(len(output), 1200)])
	}
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("Inspection HTTP must not start model")
		return nil, nil
	})
	o.HomeDir = home
	o.ConfigDir = env["CLAUDE_CONFIG_DIR"]
	o.WorkDir = home
	settings, global := protocol.NewJSONObject(), protocol.NewJSONObject()
	modelOptions := modelaccounting.NativeCalculatorOptions{Environment: env, Settings: func() (*protocol.JSONObject, error) { v := settings.Clone(); return &v, nil }, Global: func() (*protocol.JSONObject, error) { v := global.Clone(); return &v, nil }}
	o.InspectionModelOptions = &modelOptions
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	nativeSettings, err := clibootstrap.NewNativeProcessSettings(native, "darwin", clibootstrap.DefaultState(), storage.New(storage.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	defer nativeSettings.Close(context.Background())
	skills, err := skillregistry.New(skillregistry.Options{Home: home, ConfigDir: o.ConfigDir, CWD: home, ProjectRoot: home, Environment: func(key string) string { return env[key] }, Settings: func(context.Context) (protocol.JSONObject, error) { return settings.Clone(), nil }, Plugins: nativeSettings})
	if err != nil {
		t.Fatal(err)
	}
	local, err := nativeSettings.SkillCommands(clibootstrap.NativeSkillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	comparer := collate.New(language.English)
	slash, err := slashcommandsapi.New(slashcommandsapi.Options{Skills: skills, LocalCommands: func(ctx context.Context, cwd string) ([]protocol.JSONObject, error) {
		defs, err := local.Local(ctx, cwd)
		values := []protocol.JSONObject{}
		for _, def := range defs {
			values = append(values, def.Metadata())
		}
		return values, err
	}, CompiledCommands: func(context.Context) ([]protocol.JSONObject, error) {
		defs, err := skillcommands.NativeExternalBundled(skillcommands.BundledOptions{Environment: env})
		if err != nil {
			return nil, err
		}
		return sdkhost.CompiledHeadlessCommands(env, defs), nil
	}, Compare: comparer.CompareString, Warn: func(err error) { t.Error(err) }})
	if err != nil {
		t.Fatal(err)
	}
	o.SkillSlashCommands = slash.List
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for i, row := range httpCases {
		var live *inspectionTransportSession
		if active, _ := row["active"].(bool); active {
			live = &inspectionTransportSession{responses: row["responses"].(map[string]any)}
			runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
			s.runtimes[id] = &runtimeSession{ctx: runtimeCtx, cancel: runtimeCancel, live: live, options: SessionOptions{ID: id, WorkDir: home, PermissionMode: "default"}}
			if row["init"] != nil {
				s.observeConversationSDK(id, []byte(strings.ReplaceAll(string(recoveryJSON(row["init"])), "$HOME", home)))
			}
		}
		method, _ := row["method"].(string)
		if method == "" {
			method = "GET"
		}
		query, _ := row["query"].(string)
		r := httptest.NewRequest(method, "http://127.0.0.1/api/sessions/"+row["id"].(string)+"/inspection"+query, nil)
		r.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want := source.HTTPResults[i]
		var expected, actual any
		_ = json.Unmarshal(want.Body, &expected)
		_ = json.Unmarshal([]byte(strings.ReplaceAll(w.Body.String(), home, "$HOME")), &actual)
		if w.Code != want.Status || !reflect.DeepEqual(expected, actual) {
			t.Errorf("HTTP%d %s Source%d %s Go%d %s", i, query, want.Status, want.Body, w.Code, w.Body)
		}
		if live != nil {
			names := func(sent []protocol.JSONObject) []string {
				out := []string{}
				for _, request := range sent {
					out = append(out, string(recoveryJSON(request)))
				}
				sort.Strings(out)
				return out
			}
			if !reflect.DeepEqual(names(want.Sent), names(live.sent)) {
				t.Errorf("HTTP%d actual CLI controls Source%v Go%v", i, names(want.Sent), names(live.sent))
			}
			s.runtimes[id].cancel()
			delete(s.runtimes, id)
			s.resetSDKSession(id)
		}
	}
}
