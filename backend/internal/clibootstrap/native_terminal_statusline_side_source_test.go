package clibootstrap

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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceRenameSmallModelQuotaReachesProcessStatusLine(t *testing.T) {
	for _, profile := range []string{"subscriber-success", "subscriber-429", "api-key-success"} {
		t.Run(profile, func(t *testing.T) { runActualSourceRenameSmallModelQuota(t, profile) })
	}
}

func runActualSourceRenameSmallModelQuota(t *testing.T, profile string) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source required")
	}
	subscriber := profile != "api-key-success"
	status := 200
	nameJSON := `"private-side-quota"`
	headers := map[string]string{"content-type": "text/event-stream", "anthropic-ratelimit-unified-5h-utilization": "0.625", "anthropic-ratelimit-unified-5h-reset": "1700000888"}
	var first, side bytes.Buffer
	emitPlanFixture(&statusLineSideCaptureWriter{header: http.Header{}, output: &first}, 1, "", "Actual ordinary conversation")
	emitPlanFixture(&statusLineSideCaptureWriter{header: http.Header{}, output: &side}, 2, "", `{"name":"private-side-quota"}`)
	if profile == "subscriber-429" {
		status = 429
		nameJSON = "null"
		headers["content-type"] = "application/json"
		side.Reset()
		side.WriteString(`{"type":"error","error":{"type":"rate_limit_error","message":"actual side quota rejection"}}`)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(home, "statusline-script")
	if err = os.WriteFile(script, []byte("#!/bin/sh\nIFS= read -r input\nprintf '%s\\n' \"$input\" >> \"$HOME/statusline-inputs.jsonl\"\nprintf PRIVATE_SIDE_RATE\n"), 0700); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"home": home, "trusted": true, "cachedSubscriber": subscriber, "settings": map[string]any{"statusLine": map[string]any{"type": "command", "command": "'" + strings.ReplaceAll(script, "'", "'\\''") + "'"}}, "apiResponses": []any{map[string]any{"headers": map[string]string{"content-type": "text/event-stream"}, "wire": first.String()}, map[string]any{"status": status, "headers": headers, "wire": side.String()}}, "steps": []any{map[string]any{"op": "wait", "count": 1}, map[string]any{"op": "query", "text": "Actual ordinary conversation"}, map[string]any{"op": "wait", "count": 2}, map[string]any{"op": "query-name"}, map[string]any{"op": "state", "permissionMode": "plan", "value": map[string]any{}}, map[string]any{"op": "wait", "count": 3}}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-statusline.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_STATUSLINE_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1", "ANTHROPIC_API_KEY=private-source-statusline", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CLAUDE_CODE_ENTRYPOINT=cli"}
	command.Stdin = bytes.NewReader(rawMain(input))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("actual original generateSessionName/StatusLine", err, string(output))
	}
	var original struct {
		Records          []protocol.JSONObject
		PhysicalRequests []protocol.JSONObject
		GeneratedNames   []json.RawMessage
		Blocked          []string
	}
	if err = json.Unmarshal(output, &original); err != nil || len(original.Blocked) != 0 || len(original.Records) != 3 || len(original.PhysicalRequests) != 2 || len(original.GeneratedNames) != 1 || string(original.GeneratedNames[0]) != nameJSON {
		t.Fatal(err, string(output))
	}
	expected := original.Records[2].Get("rate_limits")
	if subscriber && !bytes.Contains(expected, []byte(`"used_percentage":62.5`)) || !subscriber && expected != nil {
		t.Fatal("Source original side-model quota did not reach actual callback", string(output))
	}
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if subscriber && (r.Header.Get("authorization") != "Bearer private-statusline-subscriber" || r.Header.Get("x-api-key") != "") || !subscriber && (r.Header.Get("authorization") != "" || r.Header.Get("x-api-key") != "private-statusline-fake") {
			t.Error("actual side cached subscriber HTTP", r.Header)
		}
		switch calls.Add(1) {
		case 1:
			w.Header().Set("content-type", "text/event-stream")
			_, _ = io.WriteString(w, first.String())
		case 2:
			if !bytes.Contains(body, []byte("Generate a short kebab-case name")) || !bytes.Contains(body, []byte("Actual ordinary conversation")) {
				t.Error("actual side name request missing original prompt", string(body))
			}
			for key, value := range headers {
				w.Header().Set(key, value)
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, side.String())
		default:
			t.Error("unowned side-model HTTP")
			http.Error(w, "unowned", 500)
		}
	}))
	defer peer.Close()
	_, m, _ := newNativeStatusLinePhysicalFixture(t, protocol.JSONObject{}, true, func(f nativePluginFixture) {
		f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
		f.n.Environment["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"] = "1"
		if subscriber {
			delete(f.n.Environment, "ANTHROPIC_API_KEY")
		}
		f.n.Environment["ANTHROPIC_BASE_URL"], f.n.Environment["NO_PROXY"] = peer.URL, "127.0.0.1,localhost"
		if subscriber {
			f.write(t, filepath.Join(f.config, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"private-statusline-subscriber","scopes":["user:inference","user:profile"],"expiresAt":9999999999999,"subscriptionType":"max"}}`)
			data, err := os.ReadFile(filepath.Join(f.config, ".claude.json"))
			if err != nil {
				t.Fatal(err)
			}
			global := mainObj(data)
			global.Set("oauthAccount", rawMain(map[string]string{"billingType": "stripe_subscription"}))
			f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(global)))
		}
	})
	if err = m.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", protocol.NewJSONObject(fieldMain("role", "user"), fieldMain("content", "Actual ordinary conversation"))))); err != nil {
		t.Fatal(err)
	}
	name, err := m.GenerateTerminalConversationName(ctx)
	if err != nil || string(rawMain(name)) != nameJSON {
		t.Fatal("actual native generated name", err, string(name))
	}
	cache := TerminalStatusLineCache{}
	result, err := m.RunTerminalStatusLine(ctx, "NORMAL", &cache, false)
	if err != nil || calls.Load() != 2 || string(result.Input.Get("rate_limits")) != string(expected) {
		t.Fatal("actual small-model completion quota lost before process StatusLine", err, calls.Load(), string(expected), string(result.Input.Get("rate_limits")))
	}
	t.Logf("actual original generateSessionName and canonical native small-model: two physical POSTs, original purpose/prompt/schema/subscriber gate, actual mounted shell callback %s", expected)
}

type statusLineSideCaptureWriter struct {
	header http.Header
	output *bytes.Buffer
}

func (w *statusLineSideCaptureWriter) Header() http.Header         { return w.header }
func (w *statusLineSideCaptureWriter) WriteHeader(int)             {}
func (w *statusLineSideCaptureWriter) Write(p []byte) (int, error) { return w.output.Write(p) }
