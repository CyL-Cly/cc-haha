package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPublicMCPServeBareActualStdioReadEditCacheAndErrors(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, work, tmp := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "tmp")
	for _, path := range []string{home, work, tmp, filepath.Join(home, ".claude")} {
		if err = os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	binary := filepath.Join(root, "go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	output, err := build.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT": root, "PATH": os.Getenv("PATH"), "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "ENABLE_TOOL_SEARCH": "false", "NODE_ENV": "production"}
	client, err := mcpclient.Connect(ctx, mcpclient.Config{Command: binary, Args: []string{"--bare", "mcp", "serve"}}, mcpclient.Options{Name: "native-serve", CWD: work, Environment: env, Version: version})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	listed, err := client.Request(ctx, "tools/list", protocol.NewJSONObject(), mcpclient.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var catalog []protocol.JSONObject
	if err = json.Unmarshal(listed.Get("tools"), &catalog); err != nil || len(catalog) != 3 {
		t.Fatal("actual Source bare catalogue", err, string(encodeMCP(listed)))
	}
	for i, name := range []string{"Bash", "Read", "Edit"} {
		if catalog[i].String("name") != name {
			t.Fatal("Source order", i, string(encodeMCP(listed)))
		}
	}
	path := filepath.Join(work, "proof.txt")
	if err = os.WriteFile(path, []byte("before\n"), 0640); err != nil {
		t.Fatal(err)
	}
	call := func(name string, args any) protocol.JSONObject {
		t.Helper()
		params := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: encodeMCP(name)}, protocol.JSONField{Name: "arguments", Value: encodeMCP(args)})
		value, err := client.Request(ctx, "tools/call", params, mcpclient.RequestOptions{})
		if err != nil {
			t.Fatal("actual tools/call", name, err)
		}
		return value
	}
	first := call("Edit", map[string]any{"file_path": path, "old_string": "before", "new_string": "after"})
	if string(first.Get("isError")) != "true" || !strings.Contains(string(encodeMCP(first)), "File has not been read yet") {
		t.Fatal("Source validation before read", string(encodeMCP(first)))
	}
	read := call("Read", map[string]any{"file_path": path})
	if string(read.Get("isError")) == "true" || !strings.Contains(string(encodeMCP(read)), "before") {
		t.Fatal("real Read", string(encodeMCP(read)))
	}
	edited := call("Edit", map[string]any{"file_path": path, "old_string": "before", "new_string": "after"})
	if string(edited.Get("isError")) == "true" {
		t.Fatal("shared Read cache not consumed", string(encodeMCP(edited)))
	}
	physical, err := os.ReadFile(path)
	if err != nil || string(physical) != "after\n" {
		t.Fatal("actual Edit physical side effect", err, string(physical))
	}
	var editBlocks []protocol.JSONObject
	_ = json.Unmarshal(edited.Get("content"), &editBlocks)
	if len(editBlocks) != 1 {
		t.Fatal("Source text-only result", string(encodeMCP(edited)))
	}
	var editData protocol.JSONObject
	if err = editData.UnmarshalJSON([]byte(editBlocks[0].String("text"))); err != nil || editData.String("filePath") != path || editData.String("newString") != "after" {
		t.Fatal("Source result data JSON string", err, string(encodeMCP(edited)))
	}
	unknown := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: encodeMCP("Unknown")}, protocol.JSONField{Name: "arguments", Value: json.RawMessage(`{}`)})
	if _, err = client.Request(ctx, "tools/call", unknown, mcpclient.RequestOptions{}); err == nil || !strings.Contains(err.Error(), "Tool Unknown not found") {
		t.Fatal("Source lookup stays RPC error", err)
	}
	if err = client.Close(); err != nil {
		t.Fatal("stdio cleanup", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl")); len(matches) != 0 {
		t.Fatal("MCP serve created an ASK transcript", matches)
	}
}

func TestPublicMCPServeOrdinaryActualStdioCatalogReadEditAndNoASK(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, work, tmp := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "tmp")
	for _, path := range []string{home, work, tmp, filepath.Join(home, ".claude")} {
		if err = os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	binary := filepath.Join(root, "go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	output, err := build.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT": root, "PATH": os.Getenv("PATH"), "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "ENABLE_TOOL_SEARCH": "false", "NODE_ENV": "production", "ANTHROPIC_API_KEY": "fixture-not-a-secret", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}
	client, err := mcpclient.Connect(ctx, mcpclient.Config{Command: binary, Args: []string{"mcp", "serve"}}, mcpclient.Options{Name: "native-serve", CWD: work, Environment: env, Version: version})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	listed, err := client.Request(ctx, "tools/list", protocol.NewJSONObject(), mcpclient.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var catalog []protocol.JSONObject
	if err = json.Unmarshal(listed.Get("tools"), &catalog); err != nil || len(catalog) != 20 {
		t.Fatal("actual ordinary Source catalogue", err, string(encodeMCP(listed)))
	}
	for i, name := range []string{"Agent", "TaskOutput", "Bash", "Glob", "Grep", "ExitPlanMode", "Read", "Edit", "Write", "NotebookEdit", "WebFetch", "TodoWrite", "WebSearch", "TaskStop", "AskUserQuestion", "Skill", "EnterPlanMode", "EnterWorktree", "ExitWorktree", "Workflow"} {
		if catalog[i].String("name") != name {
			t.Fatal("Source order", i, string(encodeMCP(listed)))
		}
	}
	path := filepath.Join(work, "proof.txt")
	if err = os.WriteFile(path, []byte("before\n"), 0640); err != nil {
		t.Fatal(err)
	}
	call := func(name string, args any) protocol.JSONObject {
		t.Helper()
		params := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: encodeMCP(name)}, protocol.JSONField{Name: "arguments", Value: encodeMCP(args)})
		value, err := client.Request(ctx, "tools/call", params, mcpclient.RequestOptions{})
		if err != nil {
			t.Fatal("actual tools/call", name, err)
		}
		return value
	}
	first := call("Edit", map[string]any{"file_path": path, "old_string": "before", "new_string": "after"})
	if string(first.Get("isError")) != "true" || !strings.Contains(string(encodeMCP(first)), "File has not been read yet") {
		t.Fatal("Source validation before read", string(encodeMCP(first)))
	}
	read := call("Read", map[string]any{"file_path": path})
	if string(read.Get("isError")) == "true" || !strings.Contains(string(encodeMCP(read)), "before") {
		t.Fatal("real Read", string(encodeMCP(read)))
	}
	edited := call("Edit", map[string]any{"file_path": path, "old_string": "before", "new_string": "after"})
	if string(edited.Get("isError")) == "true" {
		t.Fatal("shared Read cache not consumed", string(encodeMCP(edited)))
	}
	physical, err := os.ReadFile(path)
	if err != nil || string(physical) != "after\n" {
		t.Fatal("actual Edit physical side effect", err, string(physical))
	}
	var editBlocks []protocol.JSONObject
	_ = json.Unmarshal(edited.Get("content"), &editBlocks)
	if len(editBlocks) != 1 {
		t.Fatal("Source text-only result", string(encodeMCP(edited)))
	}
	var editData protocol.JSONObject
	if err = editData.UnmarshalJSON([]byte(editBlocks[0].String("text"))); err != nil || editData.String("filePath") != path || editData.String("newString") != "after" {
		t.Fatal("Source result data JSON string", err, string(encodeMCP(edited)))
	}
	unknown := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: encodeMCP("Unknown")}, protocol.JSONField{Name: "arguments", Value: json.RawMessage(`{}`)})
	if _, err = client.Request(ctx, "tools/call", unknown, mcpclient.RequestOptions{}); err == nil || !strings.Contains(err.Error(), "Tool Unknown not found") {
		t.Fatal("Source lookup stays RPC error", err)
	}
	if err = client.Close(); err != nil {
		t.Fatal("stdio cleanup", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl")); len(matches) != 0 {
		t.Fatal("MCP serve created an ASK transcript", matches)
	}
}
