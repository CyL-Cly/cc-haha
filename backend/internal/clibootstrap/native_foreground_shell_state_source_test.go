package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestForegroundAllUsesActualSourceShellBeforeAgentAndOneRegistry(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	m := taskMainFixture(t)
	home := m.options.Native.Home
	_ = os.MkdirAll(m.options.Native.Temp, 0700)
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-foreground-shell.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + m.options.Native.Temp, "CLAUDE_CODE_TMPDIR=" + m.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_FOREGROUND_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": m.options.Native.CWD, "states": []map[string]any{{"now": float64(1700000000000), "id": "b0123abcd", "agentId": "a0123456789abcdef", "command": "actual private shell", "description": "original foreground shell", "toolUseId": "original-shell-tool", "code": 7}}})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.5000s", e, out)
	}
	var source struct {
		States       []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.States) != 1 || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.5000s", e, out)
	}
	initial, _ := source.States[0].Object("initial")
	promoted, _ := source.States[0].Object("promoted")
	initialTasks, _ := initial.Object("tasks")
	promotedTasks, _ := promoted.Object("tasks")
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		// The task table input is the external original registration snapshot; its
		// physical lifecycle is separately exercised by the real Bash PID tests.
		order := []string{}
		for _, property := range initialTasks.OwnProperties() {
			var data protocol.JSONObject
			if e := data.UnmarshalJSON(property.Value); e != nil {
				return e
			}
			id := data.String("id")
			_, abort := context.WithCancelCause(context.Background())
			defer abort(nil)
			entry := &sessioncontroller.BackgroundTask{Data: data, Cancel: abort, ReadOutput: func(context.Context) (json.RawMessage, error) { return quoteMain(""), nil }, Join: func(context.Context) error { return nil }}
			entry.Promote = func(scope *cooperative.Scope) error {
				order = append(order, id)
				return m.Controller.UpdateBackgroundTaskOnOwner(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
					return sessioncontroller.MarkForegroundAgentBackground(task), nil
				})
			}
			if e := m.Controller.RegisterBackgroundTaskOnOwner(scope, entry); e != nil {
				return e
			}
		}
		has, e := m.Controller.HasForegroundTasksOnOwner(scope)
		if e != nil || !has || string(initial.Get("has")) != "true" {
			t.Fatal("original has foreground", has, e)
		}
		changed, e := m.Controller.BackgroundForegroundTasksOnOwner(scope)
		if e != nil || !changed {
			return e
		}
		if !reflect.DeepEqual(order, []string{"b0123abcd", "a0123456789abcdef"}) {
			t.Fatal("shell must precede earlier registered Agent", order)
		}
		rows, e := m.Controller.BackgroundTasksOnOwner(scope)
		if e != nil {
			return e
		}
		for _, data := range rows {
			var original protocol.JSONObject
			_ = original.UnmarshalJSON(promotedTasks.Get(data.String("id")))
			if string(protocol.StringifyValidatedJavaScriptJSON(rawMain(data))) != string(protocol.StringifyValidatedJavaScriptJSON(rawMain(original))) {
				t.Errorf("real registry transition Go%s Source%s", rawMain(data), rawMain(original))
			}
		}
		duplicate, e := m.Controller.BackgroundForegroundTasksOnOwner(scope)
		if e != nil || duplicate || string(source.States[0].Get("duplicate")) != "false" {
			t.Fatal("repeat background changed same task", duplicate, e)
		}
		has, e = m.Controller.HasForegroundTasksOnOwner(scope)
		if e != nil || has || string(promoted.Get("has")) != "false" {
			t.Fatal("promoted UI retained foreground", has, e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
