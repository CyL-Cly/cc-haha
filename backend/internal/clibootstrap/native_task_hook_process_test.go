package clibootstrap

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

func TestActualTaskHooksExitStatusCancellationAndOwnedProcessDrain(t *testing.T) {
	for _, profile := range []string{"exit2", "exit1", "cancel"} {
		t.Run(profile, func(t *testing.T) {
			var journal string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("task command Hook used model")
				http.Error(w, "unexpected model", 500)
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				journal = filepath.Join(n.CWD, "hook-pid.txt")
				script := filepath.Join(n.CWD, "task-process.py")
				tail := "sys.stderr.write('task-exit2-block')\nsys.exit(2)\n"
				if profile == "exit1" {
					tail = "sys.stderr.write('task-exit1-error')\nsys.exit(1)\n"
				}
				if profile == "cancel" {
					tail = "while True:time.sleep(.01)\n"
				}
				code := fmt.Sprintf("import sys,json,os,time\njson.load(sys.stdin)\nwith open(%q,'w') as f:f.write(str(os.getpid()))\n%s", journal, tail)
				if err := os.WriteFile(script, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				settings := map[string]any{"hooks": map[string]any{"TeammateIdle": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + strconv.Quote(script)}}}}}}
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read,Write"} })
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			if err := m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			hookctx, stop := context.WithCancel(ctx)
			defer stop()
			watchDone := make(chan struct{})
			if profile == "cancel" {
				go func() {
					defer close(watchDone)
					ticker := time.NewTicker(5 * time.Millisecond)
					defer ticker.Stop()
					for {
						if data, err := os.ReadFile(journal); err == nil && len(data) > 0 {
							stop()
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
						}
					}
				}()
			} else {
				close(watchDone)
			}
			blocked := []string{}
			kinds := []string{}
			err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				return m.hook.ExecuteTeammateIdleHooks(hookctx, queryhooks.NativeTaskHookInput{SessionID: m.Controller.SessionID(), TeammateName: quoteMain("actual-worker"), TeamName: quoteMain("actual-team"), Call: queryhooks.Context{Scope: scope, ComputerUseInactive: true, GoalInactive: true, TeammateInactive: true}}, func(value queryhooks.EventResult) error {
					if block, ok := value.Fields.Object("blockingError"); ok {
						feedback, err := queryhooks.FormatTaskHookFeedback("TeammateIdle", block)
						if err != nil {
							return err
						}
						blocked = append(blocked, string(feedback))
					}
					if value.Message != nil && value.Message.Type() == "attachment" {
						attachment, _ := value.Message.Record.Object("attachment")
						kinds = append(kinds, attachment.String("type"))
					}
					return nil
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			<-watchDone
			if profile == "exit2" {
				if len(blocked) != 1 || !strings.Contains(blocked[0], "task-exit2-block") || len(kinds) != 0 {
					t.Fatal("Source exit2 must block", blocked, kinds)
				}
			} else if profile == "exit1" {
				if len(blocked) != 0 || len(kinds) != 1 || kinds[0] != "hook_non_blocking_error" {
					t.Fatal("Source exit1 must remain nonblocking", blocked, kinds)
				}
			} else if len(blocked) != 0 || len(kinds) != 1 || kinds[0] != "hook_cancelled" {
				t.Fatal("Source in-flight cancel result lost", blocked, kinds)
			}
			if err = m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			process, err := os.FindProcess(pid)
			if err == nil && process.Signal(syscall.Signal(0)) == nil {
				t.Fatal("owned hook process survived actor drain and shutdown", pid)
			}
		})
	}
}
