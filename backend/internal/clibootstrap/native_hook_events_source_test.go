package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

func TestNativeHookSDKHandlerAndAllEventsGatesMatchActualSourcePrint(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	profiles := []map[string]bool{{"handler": false, "all": false}, {"handler": true, "all": false}, {"handler": false, "all": true}, {"handler": true, "all": true}}
	parent := backgroundAgentMain(t, "http://127.0.0.1:1")
	home := parent.options.Native.Home
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + parent.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "events": profiles}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct{ Rows [][]protocol.JSONObject }
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(profiles) {
		t.Fatal(e, string(out))
	}
	for i, profile := range profiles {
		rows := []protocol.JSONObject{}
		m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, func(o *MainPrintOptions) {
			o.HookEvents = profile["handler"]
			o.Arguments.IncludeHookEvents = profile["all"]
			o.OnOutput = func(bytes []byte) error {
				var message protocol.JSONObject
				if e := message.UnmarshalJSON(bytes); e != nil {
					return e
				}
				if message.String("subtype") == "hook_started" || message.String("subtype") == "hook_progress" || message.String("subtype") == "hook_response" {
					message.Set("uuid", quoteMain("source-event-uuid"))
					message.Set("session_id", quoteMain("source-session-id"))
					rows = append(rows, message)
				}
				return nil
			}
		})
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			call := queryhooks.Context{Scope: scope}
			output := queryhooks.CommandOutput{Stdout: "actual stdout", Stderr: "actual stderr", Output: "actual stdoutactual stderr", Code: 2}
			for _, kind := range []string{"started", "progress", "error"} {
				if e := m.nativeHookSDKEvent(call, kind, "actual-hook-id", "SubagentStop", output, ""); e != nil {
					return e
				}
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		if !planSourcesEqual(rawMain(rows), rawMain(source.Rows[i])) {
			t.Errorf("actual profile%d Go%s Source%s", i, rawMain(rows), rawMain(source.Rows[i]))
		}
	}
}
