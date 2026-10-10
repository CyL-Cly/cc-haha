package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualBashTimeoutBackgroundsSameProcessBeforeAndAfterForegroundRegistration(t *testing.T) {
	for _, entry := range []struct {
		name        string
		timeout     int
		interactive bool
	}{{"print-before-UI", 100, false}, {"TUI-after-registration", 3500, true}} {
		t.Run(entry.name, func(t *testing.T) {
			var command, release, pids string
			var rounds, starts atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				n := rounds.Add(1)
				if n == 1 {
					emitNativeTaskTool(w, "actual-timeout-bash", "Bash", map[string]any{"command": command, "timeout": entry.timeout, "description": "actual timeout background"})
					return
				}
				if n == 2 {
					if !strings.Contains(string(body), "Command running in background with ID:") || strings.Contains(string(body), "manually backgrounded") {
						t.Errorf("wrong Source automatic result %.2200s", body)
					}
					if e := os.WriteFile(release, []byte("release"), 0600); e != nil {
						t.Error(e)
					}
					emitPlanFixture(w, int(n), "", "TIMEOUT_PARENT_HELD")
					return
				}
				if !strings.Contains(string(body), "completed (exit code 0)") {
					t.Errorf("real completion failed to resume parent %.2200s", body)
				}
				emitPlanFixture(w, int(n), "", "ACTUAL_TIMEOUT_BACKGROUND_DONE")
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.ForegroundShellProgress = entry.interactive })
			release = filepath.Join(m.options.Native.CWD, "timeout-release")
			pids = filepath.Join(m.options.Native.CWD, "timeout-pids")
			command = "printf '%s\\n' $$ > '" + pids + "'; printf TIMEOUT_BEFORE; while ! test -f '" + release + "'; do sleep 0.05; done; printf '%s\\n' $$ >> '" + pids + "'; printf TIMEOUT_AFTER"
			var id string
			m.options.OnOutput = func(data []byte) error {
				row := taskObject(t, data)
				if row.String("type") == "system" && row.String("subtype") == "task_started" && row.String("task_type") == "local_bash" {
					id = row.String("task_id")
					starts.Add(1)
				}
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if e := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Run actual automatically backgrounded Bash"})})); e != nil {
				t.Fatal(e)
			}
			task := waitActualAgent(t, m, id)
			if task.Data.String("status") != "completed" || starts.Load() != 1 || rounds.Load() != 3 {
				t.Fatalf("actual timeout failed %+v/%d/%d", task, starts.Load(), rounds.Load())
			}
			bytes, e := os.ReadFile(pids)
			if e != nil {
				t.Fatal(e)
			}
			values := strings.Fields(string(bytes))
			if len(values) != 2 || values[0] != values[1] {
				t.Fatal("timeout respawned command", string(bytes))
			}
			output, e := os.ReadFile(task.Data.String("outputFile"))
			if e != nil || !strings.Contains(string(output), "TIMEOUT_BEFORE") || !strings.Contains(string(output), "TIMEOUT_AFTER") {
				t.Fatal(string(output), e)
			}
			config, e := m.Global.Get()
			if e != nil || string(config.Get("hasUsedBackgroundTask")) == "true" {
				t.Fatal("automatic timeout stamped user preference", e)
			}
		})
	}
}
