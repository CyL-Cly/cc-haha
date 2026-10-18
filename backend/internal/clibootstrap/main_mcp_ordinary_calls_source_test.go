package clibootstrap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestNativeMCPOrdinaryCallsFreshContextAndPhysicalEffectsActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "CLAUDE_CODE_TMPDIR": home, "PATH": "/usr/bin:/bin", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "USER_TYPE": "external", "NODE_ENV": "production", "ANTHROPIC_API_KEY": "fixture-not-a-secret", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "ENABLE_TOOL_SEARCH": "true"}
	env["USER"] = "owned-mcp-fixture"
	sourcePath, _ := filepath.Abs("../../testdata/native-mcp-ordinary.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", sourcePath)
	cmd.Dir = home
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Env = append(cmd.Env, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT="+filepath.Dir(home), "CC_HAHA_MCP_ORDINARY_SOURCE_FIXTURE=1", "CC_HAHA_MCP_SECURE_READ_RECEIPT=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	type row struct {
		Name    string
		Args    protocol.JSONObject
		Source  protocol.JSONObject
		Failure string
	}
	list := []row{}
	add := func(name string, args any) {
		var obj protocol.JSONObject
		if err := obj.UnmarshalJSON(rawMain(args)); err != nil {
			t.Fatal(err)
		}
		list = append(list, row{Name: name, Args: obj})
	}
	add("Agent", map[string]any{"description": "ordinary context", "prompt": "local fixture", "subagent_type": "general-purpose"})
	add("EnterPlanMode", map[string]any{})
	add("ExitPlanMode", map[string]any{})
	for _, text := range []string{"first", "second"} {
		add("TodoWrite", map[string]any{"todos": []any{map[string]any{"content": text, "status": "pending", "activeForm": text}}})
	}
	add("TaskOutput", map[string]any{"task_id": "fixture-missing", "block": false, "timeout": 1000})
	add("TaskStop", map[string]any{"task_id": "fixture-missing"})
	add("Skill", map[string]any{"skill": "review", "args": "7"})
	add("Skill", map[string]any{"skill": "fixture-missing"})
	add("AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Choose?", "header": "Choice", "options": []any{map[string]any{"label": "A", "description": "First"}, map[string]any{"label": "B", "description": "Second"}}, "multiSelect": false}}})
	add("WebFetch", map[string]any{"url": "not-valid", "prompt": "test"})
	add("EnterWorktree", map[string]any{"name": "ordinary-local"})
	add("ExitWorktree", map[string]any{"action": "keep"})
	add("Workflow", map[string]any{"script": "invalid script"})
	add("Read", map[string]any{"file_path": filepath.Join(home, "read.txt")})
	add("Edit", map[string]any{"file_path": filepath.Join(home, "read.txt"), "old_string": "original", "new_string": "edited"})
	add("Write", map[string]any{"file_path": filepath.Join(home, "write.txt"), "content": "owned ordinary MCP\n"})
	add("Bash", map[string]any{"command": "printf ordinary-shell", "description": "fixture output"})
	for _, command := range []string{"printf ''", "printf '\n\n  leading  \n\n'", "printf 'tabs\t\t'", "printf 'stderr' >&2"} {
		add("Bash", map[string]any{"command": command, "description": "actual whitespace boundary"})
	}
	add("ToolSearch", map[string]any{"query": "select:Read,TaskOutput,Skill", "max_results": 3})
	add("ToolSearch", map[string]any{"query": "worktree", "max_results": 2})
	backgroundMarker := filepath.Join(home, "background-finished.txt")
	add("Bash", map[string]any{"command": "/bin/sleep 0.3; printf completed > " + backgroundMarker, "description": "actual retained background", "run_in_background": true})
	add("TaskOutput", map[string]any{"task_id": "$background", "block": false, "timeout": 1000})
	add("TaskStop", map[string]any{"task_id": "$background"})
	subdir := filepath.Join(home, "cwd-subdir")
	if e := os.Mkdir(subdir, 0700); e != nil {
		t.Fatal(e)
	}
	add("Bash", map[string]any{"command": "cd " + subdir + "; pwd", "description": "actual CWD transition"})
	add("Write", map[string]any{"file_path": "cwd-proof.txt", "content": "physical relative cwd"})
	add("Bash", map[string]any{"command": "cd " + home + "; pwd", "description": "actual CWD restore"})
	add("fixture-unknown", map[string]any{})
	if err := os.WriteFile(filepath.Join(home, "read.txt"), []byte("ordinary original MCP\n"), 0600); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	sourceTaskID := ""
	for i := range list {
		if list[i].Args.String("task_id") == "$background" {
			list[i].Args.Set("task_id", quoteMain(sourceTaskID))
		}
		packet := protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: rawMain(i + 1)}, protocol.JSONField{Name: "method", Value: quoteMain("tools/call")}, protocol.JSONField{Name: "params", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(list[i].Name)}, protocol.JSONField{Name: "arguments", Value: rawMain(list[i].Args)}))})
		if _, err := input.Write(append(rawMain(packet), '\n')); err != nil {
			t.Fatal(err)
		}
		if !scanner.Scan() {
			t.Fatal("Source stdio response missing", scanner.Err(), stderr.String())
		}
		var response protocol.JSONObject
		if err := response.UnmarshalJSON(scanner.Bytes()); err != nil {
			t.Fatal(err)
		}
		if result, ok := response.Object("result"); ok {
			list[i].Source = result
			if list[i].Name == "Bash" && string(list[i].Args.Get("run_in_background")) == "true" {
				var blocks []protocol.JSONObject
				_ = json.Unmarshal(result.Get("content"), &blocks)
				var data protocol.JSONObject
				_ = data.UnmarshalJSON([]byte(blocks[0].String("text")))
				sourceTaskID = data.String("backgroundTaskId")
				if sourceTaskID == "" {
					t.Fatal("Source background ID missing")
				}
			}
		} else {
			failure, _ := response.Object("error")
			list[i].Failure = failure.String("message")
		}
	}
	_ = input.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err, stderr.String())
	}
	if data, e := os.ReadFile(filepath.Join(subdir, "cwd-proof.txt")); e != nil || string(data) != "physical relative cwd" {
		t.Fatal("actual CWD relative Write", e, string(data))
	}
	if data, err := os.ReadFile(backgroundMarker); err != nil || string(data) != "completed" {
		t.Fatal("Source EOF failed to join physical background", err, string(data))
	}
	if data, err := os.ReadFile(filepath.Join(home, "write.txt")); err != nil || string(data) != "owned ordinary MCP\n" {
		t.Fatal("Source actual Write did not persist", err, string(data))
	}
	if err := os.WriteFile(filepath.Join(home, "read.txt"), []byte("ordinary original MCP\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(home, "write.txt"))
	_ = os.Remove(backgroundMarker)
	_ = os.Remove(filepath.Join(subdir, "cwd-proof.txt"))
	startupSource := ordinaryStartupSource(t, ctx, home, env)
	n, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	var goSecureReads []securestorage.Command
	n.RunSecurity = func(_ context.Context, command securestorage.Command) ([]byte, error) {
		if len(command.Args) == 0 || command.Args[0] != "find-generic-password" || command.Input != "" {
			t.Error("non-readonly external store primitive attempted")
		}
		command.Args = append([]string(nil), command.Args...)
		goSecureReads = append(goSecureReads, command)
		return nil, os.ErrNotExist
	}
	bindCommandPrefetchFixture(t, n)
	command, err := NewMainCommand(MainCommandOptions{Arguments: []string{"mcp", "serve"}, Native: n, Platform: "darwin", Version: "fixture", PID: os.Getpid(), NonInteractive: true, Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer command.Close()
	host, err := NewNativeMCPToolContext(command, "darwin", false, false)
	if err != nil {
		t.Fatal(err)
	}
	checkOrdinaryLogicalSecureReadReceipt(t, stderr.String())
	checkOrdinaryPhysicalSecureReadReceipt(t, startupSource, goSecureReads)
	defer host.Close(context.Background())
	goTaskID := ""
	for _, row := range list {
		if row.Args.String("task_id") == sourceTaskID && sourceTaskID != "" {
			row.Args.Set("task_id", quoteMain(goTaskID))
		}
		t.Run(row.Name, func(t *testing.T) {
			got, err := host.Call(ctx, row.Name, rawMain(row.Args))
			if row.Failure != "" {
				if err == nil || err.Error() != row.Failure {
					t.Fatalf("RPC failure Go=%v Source=%s", err, row.Failure)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if row.Name == "Bash" && string(row.Args.Get("run_in_background")) == "true" {
				var blocks []protocol.JSONObject
				_ = json.Unmarshal(got.Get("content"), &blocks)
				var data protocol.JSONObject
				_ = data.UnmarshalJSON([]byte(blocks[0].String("text")))
				goTaskID = data.String("backgroundTaskId")
				if goTaskID == "" {
					t.Fatal("Go background ID missing")
				}
			}
			a, b := mcpOrdinaryResult(t, got), mcpOrdinaryResult(t, row.Source)
			if sourceTaskID != "" && goTaskID != "" {
				encoded := bytes.ReplaceAll(rawMain(a), []byte(goTaskID), []byte(sourceTaskID))
				_ = json.Unmarshal(encoded, &a)
			}
			if !reflect.DeepEqual(a, b) {
				t.Errorf("actual Source call differs Go=%v Source=%v", a, b)
			}
		})
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(backgroundMarker); err != nil || string(data) != "completed" {
		t.Fatal("Go Close failed to join physical background", err, string(data))
	}
	if data, err := os.ReadFile(filepath.Join(home, "write.txt")); err != nil || string(data) != "owned ordinary MCP\n" {
		t.Fatal("Go actual Write did not persist", err, string(data))
	}
	if data, e := os.ReadFile(filepath.Join(subdir, "cwd-proof.txt")); e != nil || string(data) != "physical relative cwd" {
		t.Fatal("Go actual CWD relative Write", e, string(data))
	}
	if data, err := os.ReadFile(filepath.Join(home, "read.txt")); err != nil || string(data) != "ordinary edited MCP\n" {
		t.Fatal("Go actual Edit did not persist", err, string(data))
	}
}
func mcpOrdinaryResult(t *testing.T, result protocol.JSONObject) any {
	t.Helper()
	var root map[string]any
	_ = json.Unmarshal(rawMain(result), &root)
	content, _ := root["content"].([]any)
	for _, block := range content {
		value, _ := block.(map[string]any)
		text, _ := value["text"].(string)
		if strings.HasPrefix(text, "{") {
			var parsed map[string]any
			if json.Unmarshal([]byte(text), &parsed) == nil {
				delete(parsed, "durationMs")
				delete(parsed, "durationSeconds")
				value["text"] = parsed
			}
		}
	}
	return root
}
