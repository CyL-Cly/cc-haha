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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSDKInitializeAgentsSourceAppendOrderInvalidMapAndDefaultMainPrintCatalog(t *testing.T) {
	f := newNativePluginFixture(t)
	oldHome := f.home
	home, e := filepath.EvalSymlinks(f.home)
	if e != nil {
		t.Fatal(e)
	}
	f.home = home
	f.cwd = home
	f.config = filepath.Join(home, ".claude")
	f.n.Home = home
	f.n.CWD = home
	f.n.Environment["HOME"] = home
	f.n.Environment["CLAUDE_CONFIG_DIR"] = f.config
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-sdk-agents"
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.write(t, filepath.Join(home, ".git", "HEAD"), "ref: refs/heads/private-sdk-agents\n")
	known, e := os.ReadFile(filepath.Join(f.config, "plugins", "known_marketplaces.json"))
	if e != nil {
		t.Fatal(e)
	}
	f.write(t, filepath.Join(f.config, "plugins", "known_marketplaces.json"), strings.ReplaceAll(string(known), oldHome, home))
	valid := map[string]any{"description": " private SDK peer ", "prompt": " physical SDK prompt ", "tools": []string{"Write", "Read"}, "model": " inherit ", "unknown": "strip"}
	rows := []map[string]any{
		{"subtype": "initialize", "agents": map[string]any{"sdk-peer": valid, "general-purpose": valid}},
		{"subtype": "initialize", "agents": map[string]any{"sdk-peer": valid, "bad": map[string]any{"description": "missing prompt"}}},
		{"subtype": "initialize", "agents": map[string]any{}},
		{"subtype": "initialize", "agents": nil},
		{"subtype": "initialize", "agents": map[string]any{"": valid}},
		{"subtype": "initialize", "agents": map[string]any{"remote": map[string]any{"description": "remote", "prompt": "remote", "isolation": "remote"}}},
		{"subtype": "initialize", "agents": map[string]any{"sdk-peer": valid, "bad": map[string]any{"description": "bad optional type", "prompt": "prompt", "initialPrompt": 7}}},
	}
	fixture, _ := filepath.Abs("../../testdata/native-sdk-initialize-options.fixture.ts")
	preload, _ := filepath.Abs("../../../preload")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	c := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", preload, fixture)
	c.Dir = home
	c.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_OPTIONS_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "ANTHROPIC_API_KEY=fake-sdk-agents", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	c.Stdin = bytes.NewReader(rawMain(map[string]any{"requests": rows, "initial": "fixture-model", "override": "fixture-model"}))
	var stderr bytes.Buffer
	c.Stderr = &stderr
	data, e := c.Output()
	if e != nil {
		t.Fatal("actual Source agents initialize", e, stderr.String())
	}
	var source struct {
		Rows         []struct{ Output []protocol.JSONObject }
		Capabilities protocol.JSONObject
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatal(e, string(data))
	}
	var blocked []string
	_ = json.Unmarshal(source.Capabilities.Get("blocked"), &blocked)
	if len(blocked) > 0 {
		t.Fatal("Source escaped", blocked)
	}
	for i, request := range rows {
		var input, output, diagnostics bytes.Buffer
		input.Write(rawMain(map[string]any{"type": "control_request", "request_id": "agents-request", "request": request}))
		input.WriteByte('\n')
		disabled := false
		m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: &input, Output: &output, Error: &diagnostics})
		if e != nil {
			t.Fatal(e)
		}
		_, runErr := m.Run(ctx)
		closeErr := m.ShutdownSession(context.Background())
		if runErr != nil || closeErr != nil {
			t.Fatalf("row %d real default MainPrint init: %v close %v %s", i, runErr, closeErr, diagnostics.String())
		}
		var frames []protocol.JSONObject
		decoder := json.NewDecoder(&output)
		for {
			var frame protocol.JSONObject
			if e = decoder.Decode(&frame); e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			frames = append(frames, frame)
		}
		var got json.RawMessage
		for _, frame := range frames {
			if frame.String("type") == "control_response" {
				response, _ := frame.Object("response")
				metadata, _ := response.Object("response")
				got = metadata.Get("agents")
			}
		}
		response, _ := source.Rows[i].Output[0].Object("response")
		metadata, _ := response.Object("response")
		var a, b any
		_ = json.Unmarshal(got, &a)
		_ = json.Unmarshal(metadata.Get("agents"), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("row %d actual Source SDK agents catalog\nGo %s\nSource %s", i, got, metadata.Get("agents"))
		}
	}
}

func TestActualSDKAgentDefaultMainPrintConsumesInitializedDefinitionInPhysicalChildRead(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	path := filepath.Join(f.cwd, "sdk-child-physical.txt")
	f.write(t, path, "ACTUAL_SDK_CHILD_READ_BYTES")
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-sdk-child"
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	var mu sync.Mutex
	parentRounds, childRounds := 0, 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
			return
		}
		var body protocol.JSONObject
		if e = body.UnmarshalJSON(data); e != nil {
			t.Error(e)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, msg := range taskBlocks(body.Get("messages")) {
			for _, block := range taskBlocks(msg.Get("content")) {
				if block.String("type") == "tool_result" && string(block.Get("is_error")) == "true" {
					t.Errorf("actual SDK Agent tool error %s", rawMain(block))
				}
			}
		}
		if strings.Contains(string(body.Get("system")), "ACTUAL_SDK_DEFINED_SYSTEM") {
			childRounds++
			if childRounds == 1 {
				emitNativeTaskTool(w, "sdk-child-read", "Read", map[string]any{"file_path": path})
			} else {
				if !strings.Contains(string(body.Get("messages")), "ACTUAL_SDK_CHILD_READ_BYTES") {
					t.Error("SDK child did not receive physical Read result")
				}
				emitPlanFixture(w, childRounds, "", "ACTUAL_SDK_CHILD_FINISHED")
			}
		} else {
			parentRounds++
			if parentRounds == 1 {
				var tools []protocol.JSONObject
				_ = json.Unmarshal(body.Get("tools"), &tools)
				available := map[string]bool{}
				for _, tool := range tools {
					available[tool.String("name")] = true
				}
				for _, name := range []string{"Agent", "Read", "Write", "Bash", "Grep", "Glob"} {
					if !available[name] {
						t.Error("ordinary default catalog lost actual tool", name)
					}
				}
				emitNativeTaskTool(w, "sdk-parent-agent", "Agent", map[string]any{"description": "Actual initialized SDK agent", "prompt": "Read the physical private file", "subagent_type": "sdk-reader"})
			} else {
				if !strings.Contains(string(body.Get("messages")), "ACTUAL_SDK_CHILD_FINISHED") {
					t.Error("parent did not consume real SDK child result")
				}
				emitPlanFixture(w, parentRounds, "", "ACTUAL_SDK_PARENT_FINISHED")
			}
		}
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	request := map[string]any{"subtype": "initialize", "agents": map[string]any{"sdk-reader": map[string]any{"description": "Read private physical input", "prompt": "ACTUAL_SDK_DEFINED_SYSTEM", "tools": []string{"Read"}, "model": "inherit"}}}
	var input, output, diagnostics bytes.Buffer
	input.Write(rawMain(map[string]any{"type": "control_request", "request_id": "sdk-agent-init", "request": request}))
	input.WriteByte('\n')
	input.Write(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Use the initialized sdk-reader Agent to read the private file"}}))
	input.WriteByte('\n')
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: &input, Output: &output, Error: &diagnostics})
	if e != nil {
		t.Fatal(e)
	}
	if m.options.Arguments.Tools != nil || m.options.Arguments.SystemPrompt != "" || m.options.Arguments.PermissionMode != "default" {
		t.Fatal("SDK child test changed ordinary default tools/system/permission conditions")
	}
	ctx, finish := context.WithTimeout(context.Background(), 30*time.Second)
	defer finish()
	_, runErr := m.Run(ctx)
	closeErr := m.ShutdownSession(context.Background())
	if runErr != nil || closeErr != nil {
		t.Fatal("default SDK Agent actual MainPrint", runErr, closeErr, diagnostics.String(), output.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if parentRounds != 2 || childRounds != 2 {
		t.Fatal("actual parent/child request consumer counts", parentRounds, childRounds, output.String())
	}
	if !strings.Contains(output.String(), "ACTUAL_SDK_PARENT_FINISHED") {
		t.Fatal("default session did not finish", output.String())
	}
}
