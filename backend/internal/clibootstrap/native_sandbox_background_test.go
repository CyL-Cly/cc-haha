//go:build !windows

package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeSandboxActualBackgroundTaskStopAndShutdownScrubJoin(t *testing.T) {
	if os.Getenv("CC_HAHA_SANDBOX_PHYSICAL_BROKER") != "1" {
		t.Skip("actual nonnested Seatbelt broker required")
	}
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("taskStop=%v", stop), func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.n.Environment["PATH"] = "/opt/homebrew/bin:/usr/bin:/bin"
			f.n.Environment["SHELL"] = "/bin/bash"
			f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
			f.write(t, filepath.Join(f.config, "settings.json"), `{"sandbox":{"enabled":true,"failIfUnavailable":true,"network":{"allowedDomains":[]}}}`)
			disabled := false
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "No inference in physical background sandbox lifecycle test", MaxTurns: 4, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			if e = m.StartSession(context.Background()); e != nil {
				t.Fatal(e)
			}
			marker := filepath.Join(f.cwd, "ready-and-child-pid.txt")
			// jobs -p is the actual shell PID producer. The installed shell-quote
			// double-quoted argument escapes ! literally; $! would test that existing
			// Source quoting behavior rather than the background lifetime.
			command := "printf planted > HEAD; mkdir -p objects; /bin/sleep 30 & jobs -p > " + bashSandboxQuote(marker) + "; wait"
			result, _ := taskToolCall(t, m, "Bash", string(rawMain(map[string]any{"command": command, "run_in_background": true, "description": "actual sandbox task cleanup"})))
			id := result.String("backgroundTaskId")
			if id == "" {
				t.Fatal("actual task was not registered", result)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
			defer cancel()
			var pid int
			for {
				bytes, e := os.ReadFile(marker)
				if e == nil {
					pid, e = strconv.Atoi(strings.TrimSpace(string(bytes)))
					if e == nil {
						break
					}
				}
				select {
				case <-ctx.Done():
					state, _ := taskToolCall(t, m, "TaskOutput", `{"task_id":"`+id+`","block":false}`)
					content, readError := os.ReadFile(marker)
					_, headError := os.Stat(filepath.Join(f.cwd, "HEAD"))
					t.Fatal("actual sandbox task did not spawn", ctx.Err(), string(rawMain(state)), "marker", string(content), readError, "HEAD", headError)
				case <-time.After(time.Millisecond):
				}
			}
			if _, e = os.Stat(filepath.Join(f.cwd, "HEAD")); e != nil {
				t.Fatal("Source bare-git scrub candidate missing before completion", e)
			}
			port, _, e := m.NativeSandboxProxyPorts(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if stop {
				_, _ = taskToolCall(t, m, "TaskStop", `{"task_id":"`+id+`"}`)
				state, _ := taskToolCall(t, m, "TaskOutput", `{"task_id":"`+id+`","block":true,"timeout":5000}`)
				if state.String("retrieval_status") != "success" {
					t.Fatal("actual task completion did not join", state)
				}
			}
			if e = m.ShutdownSession(ctx); e != nil {
				t.Fatal("actual session did not join sandbox background process", e)
			}
			if e = syscall.Kill(pid, 0); e == nil {
				t.Fatal("sandbox background child survived task/session completion", pid)
			}
			for _, name := range []string{"HEAD", "objects"} {
				if _, e = os.Stat(filepath.Join(f.cwd, name)); !os.IsNotExist(e) {
					t.Fatal("actual background completion did not scrub bare-git candidate", name, e)
				}
			}
			c, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
			if e == nil {
				c.Close()
				t.Fatal("session released owner but retained actual proxy port")
			}
		})
	}
}
