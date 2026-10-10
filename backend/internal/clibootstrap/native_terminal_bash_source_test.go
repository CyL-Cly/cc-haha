package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTerminalBashProducerAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, func(o *MainPrintOptions) { o.ForegroundShellProgress = true })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	commands := []string{"printf 'actual<&> stdout\\nsecond\\n'", "printf 'merged<&> stderr\\n' >&2", "printf 'failed<&> stderr\\n' >&2; exit 7", "true", "printf 'literal <system-reminder>visible stdout</system-reminder>'"}
	rows := []map[string]any{}
	goRows := []protocol.JSONObject{}
	for i, command := range commands {
		err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			processed, result, e := m.nativeProcessTerminalBash(ctx, scope, command, nil)
			if e != nil {
				return e
			}
			messages := []protocol.JSONObject{}
			for _, message := range processed.Messages {
				content, _ := message.Object("message")
				meta := message.Get("isMeta")
				if meta == nil {
					meta = json.RawMessage("null")
				}
				messages = append(messages, protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: content.Get("content")}, protocol.JSONField{Name: "isMeta", Value: meta}))
			}
			goRows = append(goRows, protocol.NewJSONObject(protocol.JSONField{Name: "messages", Value: rawMain(messages)}, protocol.JSONField{Name: "shouldQuery", Value: rawMain(!processed.NoQuery)}))
			row := map[string]any{"command": command, "data": json.RawMessage(result.Data)}
			if i == 2 {
				row["shellError"] = map[string]any{"stdout": result.Stdout, "stderr": result.Stderr, "code": 7, "interrupted": false}
			}
			rows = append(rows, row)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-bash.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	home := m.options.Native.Home
	temp := m.options.Native.Temp
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_TERMINAL_BASH_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source terminal producer %v %.4000s", e, out)
	}
	var source struct {
		Rows         []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(goRows) || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("Source shape %v %.4000s", e, out)
	}
	for i, row := range source.Rows {
		var calls []protocol.JSONObject
		_ = json.Unmarshal(row.Get("calls"), &calls)
		if len(calls) != 1 || calls[0].String("command") != commands[i] || string(calls[0].Get("dangerouslyDisableSandbox")) != "true" {
			t.Fatal("Source shell routing", rawMain(row))
		}
		if string(row.Get("uiStarted")) != "true" || string(row.Get("uiCleared")) != "true" {
			t.Fatal("Source UI lifecycle", rawMain(row))
		}
		row.Delete("calls")
		row.Delete("uiStarted")
		row.Delete("uiCleared")
		if !planSourcesEqual(rawMain(goRows[i]), rawMain(row)) {
			t.Errorf("case%d Go%s Source%s", i, rawMain(goRows[i]), rawMain(row))
		}
	}
}
