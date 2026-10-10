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

func TestActualInteractiveBashManuallyBackgroundsOneLiveProcessAndNotifiesHarness(t *testing.T) {
	var release, pidPath, bashCommand string
	var rounds, started atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := rounds.Add(1)
		switch n {
		case 1:
			emitNativeTaskTool(w, "actual-foreground-bash", "Bash", map[string]any{"command": bashCommand, "description": "actual Ctrl+B shell"})
		case 2:
			if !strings.Contains(string(body), "Command was manually backgrounded by user with ID:") {
				t.Errorf("actual manual result missing %.1700s", body)
			}
			if e := os.WriteFile(release, []byte("release"), 0600); e != nil {
				t.Error(e)
			}
			emitPlanFixture(w, int(n), "", "ACTUAL_SHELL_BACKGROUND_PARENT_HELD")
		default:
			if !strings.Contains(string(body), "failed with exit code 7") {
				t.Errorf("actual shell completion not returned to same Harness %.2200s", body)
			}
			emitPlanFixture(w, int(n), "", "ACTUAL_SHELL_BACKGROUND_PARENT_DONE")
		}
	}))
	defer model.Close()
	m := backgroundAgentMainWithOptions(t, model.URL, nil, func(o *MainPrintOptions) { o.ForegroundShellProgress = true })
	release = filepath.Join(m.options.Native.CWD, "actual-shell-release")
	pidPath = filepath.Join(m.options.Native.CWD, "actual-shell-pids")
	// All paths belong to the disposable working directory. The PID before and
	// after backgrounding proves this is the same real process, never a respawn.
	bashCommand = "printf '%s\\n' $$ > '" + pidPath + "'; printf BEFORE_CTRLB; while ! test -f '" + release + "'; do sleep 0.05; done; printf '%s\\n' $$ >> '" + pidPath + "'; printf AFTER_CTRLB; exit 7"
	states := make(chan bool, 8)
	current, unsubscribe, e := m.SubscribeForegroundTasks(context.Background(), func(v bool) { states <- v })
	if e != nil || current {
		t.Fatal(current, e)
	}
	defer unsubscribe()
	var id string
	m.options.OnOutput = func(data []byte) error {
		row := taskObject(t, data)
		if row.String("type") == "system" && row.String("subtype") == "task_started" && row.String("task_type") == "local_bash" {
			id = row.String("task_id")
			started.Add(1)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Run the actual interactive foreground Bash"})}))
	}()
	select {
	case v := <-states:
		if !v {
			t.Fatal("missing actual foreground shell")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	has, e := m.HasForegroundTasks(ctx)
	if e != nil || !has {
		t.Fatal(has, e)
	}
	changed, e := m.BackgroundFromTerminal(ctx)
	if e != nil || !changed {
		t.Fatal(changed, e)
	}
	select {
	case v := <-states:
		if v {
			t.Fatal("background shell kept foreground keybinding")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	task := waitActualAgent(t, m, id)
	if started.Load() != 1 || rounds.Load() != 3 || task.Data.String("status") != "failed" || string(task.Data.Get("isBackgrounded")) != "true" {
		t.Fatalf("actual transition %d/%d %s", started.Load(), rounds.Load(), rawMain(task.Data))
	}
	result, _ := task.Data.Object("result")
	if string(result.Get("code")) != "7" {
		t.Fatal("physical exit code lost", rawMain(task.Data))
	}
	pids, e := os.ReadFile(pidPath)
	if e != nil {
		t.Fatal(e)
	}
	rows := strings.Fields(string(pids))
	if len(rows) != 2 || rows[0] != rows[1] {
		t.Fatal("command was respawned", string(pids))
	}
	output, e := os.ReadFile(task.Data.String("outputFile"))
	if e != nil || !strings.Contains(string(output), "BEFORE_CTRLB") || !strings.Contains(string(output), "AFTER_CTRLB") {
		t.Fatal("output before/after promotion lost", string(output), e)
	}
	config, e := m.Global.Get()
	if e != nil || string(config.Get("hasUsedBackgroundTask")) != "true" {
		t.Fatal("actual terminal preference not persisted", e)
	}
}
