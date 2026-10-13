package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPublicComputerUseMCPActualProductionChildNoASKOrNativeInput(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("original CU supported platforms")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, work, tmp := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "tmp")
	config := filepath.Join(home, ".claude")
	for _, path := range []string{home, work, tmp, filepath.Join(config, "cc-haha")} {
		if err = os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(config, "cc-haha", "computer-use-config.json")
	before := []byte(`{"enabled":true,"future":{"keep":"stdio-production"}}`)
	if err = os.WriteFile(configPath, before, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	binary := filepath.Join(root, "cu-go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": tmp, "PATH": os.Getenv("PATH"), "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "NODE_ENV": "production"}
	defaultEnv := make(map[string]string, len(env))
	for key, value := range env {
		if key != "CLAUDE_CONFIG_DIR" {
			defaultEnv[key] = value
		}
	}
	defaultClient, err := mcpclient.Connect(ctx, mcpclient.Config{Command: binary, Args: []string{"--computer-use-mcp"}}, mcpclient.Options{Name: "computer-use-default-config", CWD: work, Environment: defaultEnv, Version: "fixture-client"})
	if err != nil {
		t.Fatal("default home config failed to initialize production child", err)
	}
	defaultListed, defaultErr := defaultClient.Request(ctx, "tools/list", protocol.NewJSONObject(), mcpclient.RequestOptions{})
	closeErr := defaultClient.Close()
	if defaultErr != nil || closeErr != nil {
		t.Fatal("default home tools/list", defaultErr, closeErr)
	}
	client, err := mcpclient.Connect(ctx, mcpclient.Config{Command: binary, Args: []string{"--computer-use-mcp"}}, mcpclient.Options{Name: "computer-use", CWD: work, Environment: env, Version: "fixture-client"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	listed, err := client.Request(ctx, "tools/list", protocol.NewJSONObject(), mcpclient.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var tools []protocol.JSONObject
	expected := 2
	if runtime.GOOS == "windows" {
		expected = 22
	}
	if err = json.Unmarshal(listed.Get("tools"), &tools); err != nil || len(tools) != expected {
		t.Fatal(err, string(encodeMCP(listed)))
	}
	if string(defaultListed.Get("tools")) != string(listed.Get("tools")) {
		t.Fatal("default home config lost enabled original tool catalogue", string(encodeMCP(defaultListed)))
	}
	call := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: encodeMCP("screenshot")}, protocol.JSONField{Name: "arguments", Value: json.RawMessage(`{}`)})
	result, err := client.Request(ctx, "tools/call", call, mcpclient.RequestOptions{})
	if err != nil || string(result.Get("isError")) != "true" || !strings.Contains(string(encodeMCP(result)), "not wired to a session") {
		t.Fatal(err, string(encodeMCP(result)))
	}
	if err = os.WriteFile(configPath, []byte(`{"enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	listed, err = client.Request(ctx, "tools/list", protocol.NewJSONObject(), mcpclient.RequestOptions{})
	if err != nil || string(listed.Get("tools")) != "[]" {
		t.Fatal(err, string(encodeMCP(listed)))
	}
	if err = client.Close(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := filepath.Glob(filepath.Join(config, "projects", "*", "*.jsonl")); len(entries) != 0 {
		t.Fatal("unbound catalog entry created model transcript", entries)
	}
	if _, err = os.Stat(filepath.Join(config, "computer-use.lock")); !os.IsNotExist(err) {
		t.Fatal("unbound tools acquired global input lease", err)
	}
	if _, err = os.Stat(filepath.Join(config, ".runtime")); !os.IsNotExist(err) {
		t.Fatal("tools/call stub launched or configured native transport", err)
	}
}
