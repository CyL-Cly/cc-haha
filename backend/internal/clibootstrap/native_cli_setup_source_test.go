package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeCLISetupForceSyncOrderingAndOriginalInputSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	for _, row := range []struct {
		name, trigger                          string
		only, interactive, dynamic, configured bool
	}{
		{name: "print-init", trigger: "init"}, {name: "print-maintenance", trigger: "maintenance"},
		{name: "init-only-configured-async", trigger: "init", only: true, configured: true},
		{name: "init-only-dynamic-async", trigger: "init", only: true, dynamic: true},
		{name: "interactive-init-source-no-setup", trigger: "init", interactive: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			var journal, finished string
			var settings map[string]any
			m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				journal = filepath.Join(n.CWD, "setup-journal.jsonl")
				finished = filepath.Join(n.CWD, "setup-finished")
				script := filepath.Join(n.CWD, "setup.py")
				source := fmt.Sprintf("import sys,json,time\nfrom pathlib import Path\nx=json.load(sys.stdin)\nwith open(%q,'a') as f:f.write(json.dumps(x)+'\\n')\nif x['hook_event_name']=='Setup':\n if %t: print(json.dumps({'async':True}),flush=True)\n time.sleep(.04)\n Path(%q).write_text('completed')\nelse:\n assert Path(%q).exists() or %t\nprint(json.dumps({'hookSpecificOutput':{'hookEventName':x['hook_event_name'],'additionalContext':'SOURCE_SETUP_CONTEXT'}}),flush=True)\n", journal, row.dynamic, finished, finished, row.interactive)
				source = strings.ReplaceAll(strings.ReplaceAll(source, "if true:", "if True:"), "if false:", "if False:")
				source = strings.ReplaceAll(strings.ReplaceAll(source, "or true", "or True"), "or false", "or False")
				if e := os.WriteFile(script, []byte(source), 0600); e != nil {
					t.Fatal(e)
				}
				hook := map[string]any{"type": "command", "command": "/usr/bin/python3 " + settingsShellQuote(script)}
				if row.configured {
					hook["async"] = true
				}
				settings = map[string]any{
					"alwaysThinkingEnabled": false, "agentTeamsEnabled": false,
					"hooks": map[string]any{
						"Setup": []any{
							map[string]any{"matcher": row.trigger, "hooks": []any{hook}},
							map[string]any{"matcher": "never", "hooks": []any{map[string]any{"type": "command", "command": "exit 99"}}},
						},
						"SessionStart": []any{
							map[string]any{"matcher": "startup", "hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + settingsShellQuote(script)}}},
						},
					},
				}
				if e := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); e != nil {
					t.Fatal(e)
				}
			}, func(o *MainPrintOptions) {
				o.PublicPrintLoadErrors = !row.interactive
				o.Interactive = row.interactive
				o.Arguments.InitOnly = row.only
				o.Arguments.Init = row.trigger == "init"
				o.Arguments.Maintenance = row.trigger == "maintenance"
			})
			data, e := os.ReadFile(journal)
			if e != nil {
				t.Fatal(e)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			wanted := 2
			if row.interactive {
				wanted = 1
			}
			if len(lines) != wanted {
				t.Fatal("physical startup event count", string(data))
			}
			var setup protocol.JSONObject
			if e = setup.UnmarshalJSON([]byte(lines[0])); e != nil {
				t.Fatal(e)
			}
			if row.interactive {
				if setup.String("hook_event_name") != "SessionStart" {
					t.Fatal(setup)
				}
				return
			}
			if _, e = os.Stat(finished); e != nil {
				t.Fatal("init-only returned before async process completed", e)
			}
			home := t.TempDir()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-setup.fixture.ts")
			base := map[string]any{"session_id": m.Controller.SessionID(), "transcript_path": setup.String("transcript_path"), "cwd": setup.String("cwd")}
			hooks := settings["hooks"].(map[string]any)
			setupSettings := map[string]any{"hooks": map[string]any{"Setup": hooks["Setup"]}}
			commandText := hooks["Setup"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)
			input := map[string]any{"homeDir": home, "rows": []any{map[string]any{"source": row.trigger, "base": base, "settings": setupSettings, "forceSyncExecution": row.only, "outputs": map[string]any{commandText: map[string]any{"stdout": "actual primitive output", "code": 0}}}}}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			c := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			c.Dir = home
			c.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "BUN_OPTIONS=--no-env-file", "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_SETUP_FIXTURE=1", "PATH=/usr/bin:/bin"}
			c.Stdin = bytes.NewReader(rawMain(input))
			out, e := c.CombinedOutput()
			if e != nil {
				t.Fatal(e, string(out))
			}
			var actual struct {
				Rows []struct{ Input protocol.JSONObject }
			}
			if e = json.Unmarshal(out, &actual); e != nil || len(actual.Rows) != 1 {
				t.Fatal(e, string(out))
			}
			sourceInput := actual.Rows[0].Input
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(rawMain(sourceInput)), protocol.StringifyValidatedJavaScriptJSON(rawMain(setup))) {
				var s, g map[string]any
				_ = json.Unmarshal(rawMain(sourceInput), &s)
				_ = json.Unmarshal(rawMain(setup), &g)
				if fmt.Sprint(s) != fmt.Sprint(g) {
					t.Fatalf("actual Setup input differs Source %s Go %s", rawMain(sourceInput), rawMain(setup))
				}
			}
		})
	}
}
