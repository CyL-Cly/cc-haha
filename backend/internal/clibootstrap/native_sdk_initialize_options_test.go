package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSDKInitializeOptionsOriginalSourceThenDefaultMainPrintStructuredOutput(t *testing.T) {
	f := newNativePluginFixture(t)
	oldHome := f.home
	home, e := filepath.EvalSymlinks(f.home)
	if e != nil {
		t.Fatal(e)
	}
	f.home = home
	f.config = filepath.Join(home, ".claude")
	f.cwd = home
	f.n.Home = home
	f.n.CWD = f.cwd
	f.n.Environment["HOME"] = home
	f.n.Environment["CLAUDE_CONFIG_DIR"] = f.config
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-sdk-options"
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	known, e := os.ReadFile(filepath.Join(f.config, "plugins", "known_marketplaces.json"))
	if e != nil {
		t.Fatal(e)
	}
	f.write(t, filepath.Join(f.config, "plugins", "known_marketplaces.json"), strings.ReplaceAll(string(known), oldHome, home))
	f.write(t, filepath.Join(home, ".git", "HEAD"), "ref: refs/heads/private-sdk-options\n")
	schema := map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}, "required": []string{"answer"}, "additionalProperties": false}
	request := map[string]any{"subtype": "initialize", "systemPrompt": "ACTUAL_SDK_CUSTOM_OPTIONS", "appendSystemPrompt": "ACTUAL_SDK_APPEND_OPTIONS", "jsonSchema": schema}
	fixture, _ := filepath.Abs("../../testdata/native-sdk-initialize-options.fixture.ts")
	preload, _ := filepath.Abs("../../../preload")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, stop := context.WithTimeout(context.Background(), 25*time.Second)
	defer stop()
	sourceCmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", preload, fixture)
	sourceCmd.Dir = home
	sourceCmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_OPTIONS_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "ANTHROPIC_API_KEY=fake-sdk-options", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	sourceCmd.Stdin = bytes.NewReader(rawMain(map[string]any{"request": request, "initial": "fixture-model", "override": "fixture-model", "options": map[string]any{"systemPrompt": "BEFORE_SDK_CUSTOM", "appendSystemPrompt": "BEFORE_SDK_APPEND"}}))
	var sourceErrors bytes.Buffer
	sourceCmd.Stderr = &sourceErrors
	data, e := sourceCmd.Output()
	if e != nil {
		t.Fatal("actual original initialize", e, sourceErrors.String())
	}
	var source struct {
		SystemPrompt []string
		SystemWire   []string
		JSONSchema   any
		Output       []protocol.JSONObject
		Capabilities protocol.JSONObject
	}
	if e = json.Unmarshal(data, &source); e != nil {
		t.Fatal(e, string(data))
	}
	if len(source.SystemPrompt) != 2 {
		t.Fatal("original initialize did not replace live options", string(data))
	}
	var blocked []string
	_ = json.Unmarshal(source.Capabilities.Get("blocked"), &blocked)
	if len(blocked) > 0 {
		t.Fatal("Source capability escape", blocked)
	}
	var expectedSchema any
	_ = json.Unmarshal(rawMain(schema), &expectedSchema)
	if !reflect.DeepEqual(source.JSONSchema, expectedSchema) {
		t.Fatal("original SDK schema state", source.JSONSchema)
	}
	var mu sync.Mutex
	var bodies []protocol.JSONObject
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
			return
		}
		var value protocol.JSONObject
		if e = value.UnmarshalJSON(body); e != nil {
			t.Error(e)
			return
		}
		mu.Lock()
		bodies = append(bodies, value.Clone())
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			emitWorktreeModel(w, n, "StructuredOutput", `{"answer":"actual-sdk-options"}`, "")
		} else if n == 2 {
			emitWorktreeModel(w, n, "", "", "actual SDK options consumed")
		} else {
			t.Errorf("unexpected model request %d", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	var output, diagnostics bytes.Buffer
	disabled := false
	frames := []map[string]any{{"type": "control_request", "request_id": "init-options", "request": request}, {"type": "user", "message": map[string]any{"role": "user", "content": "Use the StructuredOutput tool with the actual answer"}}}
	var input bytes.Buffer
	for _, row := range frames {
		input.Write(rawMain(row))
		input.WriteByte('\n')
	}
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "BEFORE_SDK_CUSTOM", AppendSystemPrompt: "BEFORE_SDK_APPEND", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: &input, Output: &output, Error: &diagnostics})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if _, e = m.Run(ctx); e != nil {
		t.Fatal("actual SDK options default MainPrint consumer", e, diagnostics.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatal("actual schema/model request count", len(bodies), output.String())
	}
	for _, body := range bodies {
		var sections []protocol.JSONObject
		_ = json.Unmarshal(body.Get("system"), &sections)
		var texts []string
		for _, s := range sections {
			texts = append(texts, s.String("text"))
		}
		if len(texts) != len(source.SystemWire)+1 || !reflect.DeepEqual(texts[1:], source.SystemWire) || strings.Contains(strings.Join(texts, "\n"), "BEFORE_SDK") {
			t.Fatal("model did not consume actual Source initialization options", texts, source.SystemWire)
		}
		var tools []protocol.JSONObject
		_ = json.Unmarshal(body.Get("tools"), &tools)
		found := false
		for _, tool := range tools {
			found = found || tool.String("name") == "StructuredOutput"
		}
		if !found {
			t.Fatal("SDK schema absent from ordinary tool catalog")
		}
	}
	decoder := json.NewDecoder(&output)
	var final protocol.JSONObject
	for {
		var event protocol.JSONObject
		e = decoder.Decode(&event)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if event.String("type") == "result" {
			final = event
		}
	}
	if string(final.Get("structured_output")) != `{"answer":"actual-sdk-options"}` || string(final.Get("is_error")) != "false" {
		t.Fatal(fmt.Sprintf("actual structured SDK result %s", rawMain(final)))
	}
}
