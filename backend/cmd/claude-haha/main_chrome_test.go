package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/chromenative"
)

func TestChromeFastPathOnlyFirstArgumentAndFrozenEmptyMCP(t *testing.T) {
	t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
	for _, args := range [][]string{{"--claude-in-chrome-mcp"}, {"--claude-in-chrome-mcp", "--settings", "missing.json"}, {"--claude-in-chrome-mcp", "--not-a-flag"}} {
		if code, handled := runChromeFastPath(context.Background(), args); code != 0 || !handled {
			t.Fatal(args, code, handled)
		}
	}
	for _, args := range [][]string{nil, {"--print", "--chrome-native-host"}, {"cli", "--claude-in-chrome-mcp"}} {
		if _, handled := runChromeFastPath(context.Background(), args); handled {
			t.Fatal("Chrome fast path moved past argv[2]", args)
		}
	}
}

func TestPublicChromeNativeHostProductionBinaryPingStatusAndEmptyMCP(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	executable := filepath.Join(root, "native-chrome-go-cli")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-o", executable, ".")
	capturePublicGoCoverage(t, build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + root, "PATH=" + os.Getenv("PATH"), "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	stub := exec.CommandContext(ctx, executable, "--claude-in-chrome-mcp", "--settings", "must-not-be-loaded.json")
	stub.Env = env
	capturePublicGoCoverage(t, stub)
	if output, err := stub.Output(); err != nil || len(output) != 0 {
		t.Fatal("shipped empty MCP became a browser server or ordinary CLI", err, string(output))
	}
	cmd := exec.CommandContext(ctx, executable, "--chrome-native-host", "--settings", "must-not-be-loaded.json")
	cmd.Env = env
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
	capturePublicGoCoverage(t, cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	write := func(value string) {
		data := []byte(value)
		var prefix [4]byte
		binary.LittleEndian.PutUint32(prefix[:], uint32(len(data)))
		if _, err := input.Write(append(prefix[:], data...)); err != nil {
			t.Fatal(err)
		}
	}
	read := func() map[string]any {
		var prefix [4]byte
		if _, err := io.ReadFull(output, prefix[:]); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, binary.LittleEndian.Uint32(prefix[:]))
		if _, err := io.ReadFull(output, data); err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if json.Unmarshal(data, &value) != nil {
			t.Fatal(string(data))
		}
		return value
	}
	write(`{"type":"ping"}`)
	if response := read(); response["type"] != "pong" || response["timestamp"] == nil {
		t.Fatal(response)
	}
	write(`{"type":"get_status"}`)
	if response := read(); response["type"] != "status_response" || response["native_host_version"] != chromenative.Version {
		t.Fatal(response)
	}
	path := chromenative.SocketPath(chromenative.Username(map[string]string{}), "", cmd.Process.Pid)
	_ = input.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err, stderr.String())
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("public native child leaked socket", path, err)
		}
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatal("fast path loaded ordinary user configuration", entries, err)
	}
}
