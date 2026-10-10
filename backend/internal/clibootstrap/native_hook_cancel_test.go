//go:build darwin || linux

package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualPreToolHookCancellationJoinsPhysicalProcessAndDoesNotExecuteWrite(t *testing.T) {
	var file, marker string
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		posts.Add(1)
		emitNativeTaskTool(w, "cancel-hook-write", "Write", map[string]any{"file_path": file, "content": "must not execute"})
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		file = filepath.Join(n.CWD, "must-not-write.txt")
		marker = filepath.Join(n.CWD, "hook-pid.txt")
		script := filepath.Join(n.CWD, "cancel-hook.py")
		code := fmt.Sprintf("import json,sys,time,os\nx=json.load(sys.stdin)\nwith open(%q,'w') as f:f.write(str(os.getpid()))\ntime.sleep(60)\nprint(json.dumps({'decision':'approve'}))\n", marker)
		if err := os.WriteFile(script, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Write", "hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + strconv.Quote(script)}}}}}}), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Write"} })
	turn, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(turn, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "cancel actual Hook"})}))
	}()
	deadline := time.Now().Add(10 * time.Second)
	pid := 0
	for pid == 0 && time.Now().Before(deadline) {
		data, _ := os.ReadFile(marker)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		if pid == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if pid == 0 {
		t.Fatal("physical Hook never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("canceled actual Hook did not drain")
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("canceled pre Hook executed Write", err)
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatal("physical Hook survived shared shutdown", pid, err)
	}
	if posts.Load() != 1 {
		t.Fatal("cancellation entered another model turn", posts.Load())
	}
	canceled, feedback := 0, 0
	for _, message := range m.Controller.History() {
		if a, ok := message.Object("attachment"); ok && a.String("type") == "hook_cancelled" {
			canceled++
		}
		if message.String("type") != "user" {
			continue
		}
		api, _ := message.Object("message")
		for _, block := range taskBlocks(api.Get("content")) {
			if block.String("type") == "tool_result" && block.String("tool_use_id") == "cancel-hook-write" {
				feedback++
				if block.String("content") != "The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed." || string(block.Get("is_error")) != "true" || message.String("toolUseResult") != "Error: undefined" {
					t.Fatalf("pre Hook stop did not use original cancellation feedback: %s", rawMain(message))
				}
			}
		}
	}
	if canceled != 2 || feedback != 1 {
		t.Fatal("original runPreToolUseHooks cancellation sequence", canceled, feedback)
	}
}
