package clibootstrap

import (
	"context"
	"fmt"
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

func TestActualBashCwdChangesSharedToolsRunsHookAndRebasesWatchPaths(t *testing.T) {
	for _, foreground := range []bool{false, true} {
		t.Run(fmt.Sprint(foreground), func(t *testing.T) {
			var calls atomic.Int32
			var old, target, journal string
			notified := make(chan string, 10)
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				switch calls.Add(1) {
				case 1:
					emitNativeTaskTool(w, "cwd-cd", "Bash", map[string]any{"command": "cd '" + target + "'"})
				case 2:
					emitNativeTaskTool(w, "cwd-write", "Write", map[string]any{"file_path": "physical-relative.txt", "content": "REAL_CWD_FILE_A21"})
				case 3:
					emitNativeTaskTool(w, "cwd-read", "Read", map[string]any{"file_path": "physical-relative.txt"})
				case 4:
					if !strings.Contains(string(body), "REAL_CWD_FILE_A21") {
						t.Error("Read bytes absent")
					}
					emitPlanFixture(w, 4, "", "CWD_FINISHED")
				default:
					t.Error("unexpected model turn")
				}
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				old = n.CWD
				target = filepath.Join(n.CWD, "new-work")
				journal = filepath.Join(n.CWD, "cwd-hook.json")
				if err := os.MkdirAll(target, 0700); err != nil {
					t.Fatal(err)
				}
				command := `cat > "` + journal + `"; printf '%s' '{"systemMessage":"REAL_CWD_NOTICE_A21"}'`
				settings := map[string]any{"hooks": map[string]any{"CwdChanged": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}}, "FileChanged": []any{map[string]any{"matcher": "relative.env", "hooks": []any{}}}}}
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) {
				o.ForegroundShellProgress = foreground
				o.OnEnvironmentHookMessage = func(text string, _ bool) { notified <- text }
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "change physical cwd and use relative tools"})})
			if err := m.RunTurn(ctx, user); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(filepath.Join(target, "physical-relative.txt")); err != nil || string(data) != "REAL_CWD_FILE_A21" {
				t.Fatal("actual relative Write after Bash cd", err, string(data))
			}
			if _, err := os.Stat(filepath.Join(old, "physical-relative.txt")); !os.IsNotExist(err) {
				t.Fatal("wrote old cwd", err)
			}
			select {
			case text := <-notified:
				if text != "REAL_CWD_NOTICE_A21" {
					t.Fatal(text)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("actual CwdChanged absent")
			}
			data, err := os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			var input protocol.JSONObject
			_ = input.UnmarshalJSON(data)
			if input.String("cwd") != target || input.String("old_cwd") != old || input.String("new_cwd") != target {
				t.Fatal(string(data))
			}
			if paths, err := filepath.Glob(filepath.Join(m.options.Native.Temp, "claude-*-cwd")); err != nil || len(paths) != 0 {
				t.Fatal("actual cwd tempfile cleanup", paths, err)
			}
		})
	}
}
