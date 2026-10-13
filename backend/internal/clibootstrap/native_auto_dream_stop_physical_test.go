package clibootstrap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNativeAutoDreamDialogKillDesktopStopAndCloseJoinHTTPAndRollback(t *testing.T) {
	for _, kind := range []string{"dialog", "desktop-stop", "runtime-close"} {
		t.Run(kind, func(t *testing.T) {
			opened, closed := make(chan struct{}), make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if !bytes.Contains(body, []byte("# Dream: Memory Consolidation")) {
					emitPlanFixture(w, 1, "", "ACTUAL_PARENT_DONE")
					return
				}
				close(opened)
				<-r.Context().Done()
				close(closed)
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"autoDreamEnabled":true,"enabledPlugins":{"sample@mkt":false}}`), 0600); err != nil {
					t.Fatal(err)
				}
				project := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(n.CWD))
				if err := os.MkdirAll(project, 0700); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 5; i++ {
					if err := os.WriteFile(filepath.Join(project, fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d.jsonl", i)), []byte("{}\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}, func(o *MainPrintOptions) { o.Interactive = true })
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "actual parent for background dream"})})); err != nil {
				t.Fatal(err)
			}
			select {
			case <-opened:
			case <-ctx.Done():
				t.Fatal("actual Dream HTTP not opened", ctx.Err())
			}
			var row protocol.JSONObject
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				rows, err := m.Controller.BackgroundTasksOnOwner(scope)
				for _, candidate := range rows {
					if candidate.String("type") == "dream" {
						row = candidate
					}
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if row.String("status") != "running" {
				t.Fatal("actual pending task missing")
			}
			var memory string
			if err := m.Owner.Run(ctx, func(*cooperative.Scope) error { var err error; memory, err = m.memoryPaths.Directory(); return err }); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(memory, ".consolidate-lock")); err != nil {
				t.Fatal("actual lease missing", err)
			}
			var err error
			switch kind {
			case "dialog":
				err = m.StopNativeAutoDream(ctx, row.String("id"))
			case "desktop-stop":
				err = m.StopAllBackgroundTasks(ctx)
			case "runtime-close":
				err = m.ShutdownSession(ctx)
			}
			if err != nil {
				t.Fatal("actual stop boundary", err)
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("Stop returned without physical HTTP cancellation/join", ctx.Err())
			}
			if _, err := os.Stat(filepath.Join(memory, ".consolidate-lock")); !os.IsNotExist(err) {
				t.Fatal("failed/stopped dream did not rollback zero-prior lease", err)
			}
			if kind != "runtime-close" {
				var entryJoin func(context.Context) error
				if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					entry, err := m.Controller.BackgroundTaskOnOwner(scope, row.String("id"))
					if entry != nil {
						row = entry.Data
						entryJoin = entry.Join
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if entryJoin == nil || entryJoin(ctx) != nil {
					t.Fatal("actual native continuation did not join")
				}
				want := "killed"
				if kind == "desktop-stop" {
					want = "failed"
				}
				if row.String("status") != want || string(row.Get("notified")) != "true" {
					t.Fatal("incorrect or overwritten terminal", string(rawMain(row)))
				}
				if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "actual same owner after dream stop"})})); err != nil {
					t.Fatal("Stop damaged root continuation", err)
				}
			}
		})
	}
}
