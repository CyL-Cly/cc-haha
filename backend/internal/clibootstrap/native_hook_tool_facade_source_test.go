package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNativeToolHookAttachmentsMatchOriginalCompletionAggregationAndConsumers(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	profiles := []struct {
		pre, post string
		writes    bool
	}{
		{`{}`, `{}`, true},
		{`{"systemMessage":"UI pre","hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"pre context"}}`, `{"systemMessage":"UI post","hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"post context"}}`, true},
		{`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"approved"}}`, `{}`, true},
		{`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"denied"}}`, `{}`, false},
		{`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"asked"}}`, `{}`, false},
		{`{"decision":"block","reason":"legacy block"}`, `{}`, false},
		{`{"continue":false,"stopReason":"pre stop","hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"after pre stop"}}`, `{}`, true},
		{`{}`, `{"decision":"block","reason":"post block","systemMessage":"post UI","hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"after post block"}}`, true},
		{`{}`, `{"continue":false,"stopReason":"post stop","systemMessage":"not reached","hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"not reached"}}`, true},
		{`{"continue":false,"stopReason":"pre stop","hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"before context"}}`, `{"continue":false,"stopReason":"post stop"}`, true},
	}
	for index, p := range profiles {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			var file, command string
			var posts atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if posts.Add(1) == 1 {
					emitNativeTaskTool(w, "facade-tool-use", "Write", map[string]any{"file_path": file, "content": "actual facade file"})
					return
				}
				emitPlanFixture(w, 2, "", "SOURCE_FACADE_DONE")
			}))
			defer model.Close()
			m := backgroundAgentMainWithOptions(t, model.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				file = filepath.Join(n.CWD, "facade-file.txt")
				script := filepath.Join(n.CWD, "facade-hook.py")
				command = "/usr/bin/python3 " + strconv.Quote(script)
				code := fmt.Sprintf("import json,sys\nx=json.load(sys.stdin)\nsys.stdout.write((%q if x['hook_event_name']=='PreToolUse' else %q)+'\\n')\n", p.pre, p.post)
				if err := os.WriteFile(script, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				hooks := map[string]any{}
				for _, event := range []string{"PreToolUse", "PostToolUse"} {
					hooks[event] = []any{map[string]any{"matcher": "Write", "hooks": []any{map[string]any{"type": "command", "command": command}}}}
				}
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"hooks": hooks}), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Write"} })
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "actual Source facade"})})); err != nil {
				t.Fatal(err)
			}
			actual := []protocol.JSONObject{}
			for _, message := range m.Controller.History() {
				if message.String("type") == "attachment" {
					a, ok := message.Object("attachment")
					if ok && strings.HasPrefix(a.String("type"), "hook_") {
						a.Delete("durationMs")
						actual = append(actual, a)
					}
				}
			}
			n := m.options.Native
			if err := os.MkdirAll(n.Temp, 0700); err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = n.Home
			cmd.Env = []string{"HOME=" + n.Home, "USERPROFILE=" + n.Home, "CLAUDE_CONFIG_DIR=" + n.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + n.Temp, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(n.Home), "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": n.Home, "toolFacades": []any{map[string]any{"toolName": "Write", "toolUseID": "facade-tool-use", "executePost": p.writes, "input": map[string]string{"file_path": file, "content": "actual facade file"}, "outputs": map[string]any{"PreToolUse": []any{map[string]string{"stdout": p.pre + "\n", "command": command}}, "PostToolUse": []any{map[string]string{"stdout": p.post + "\n", "command": command}}}}}}))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var source struct {
				Observations []struct{ Attachments []protocol.JSONObject }
			}
			if err = json.Unmarshal(out, &source); err != nil || len(source.Observations) != 1 {
				t.Fatal(err, string(out))
			}
			if !planSourcesEqual(rawMain(actual), rawMain(source.Observations[0].Attachments)) {
				t.Errorf("original aggregation/facade mismatch Go%s Source%s", rawMain(actual), rawMain(source.Observations[0].Attachments))
			}
			_, err = os.Stat(file)
			if p.writes != (err == nil) {
				t.Fatal("actual physical tool outcome", p.writes, err)
			}
			if err = m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
