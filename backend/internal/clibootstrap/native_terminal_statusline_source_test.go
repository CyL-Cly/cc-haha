package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceMountedStatusLinePhysicalShellJSONDebounceAndTrust(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	for _, profile := range []string{"mounted", "user-disabled", "untrusted"} {
		t.Run(profile, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, directory := range []string{".claude", "tmp"} {
				if err = os.MkdirAll(filepath.Join(home, directory), 0700); err != nil {
					t.Fatal(err)
				}
			}
			script := filepath.Join(home, "statusline-script")
			program := "#!/bin/sh\nIFS= read -r input\nprintf '%s\\n' \"$input\" >> \"$HOME/statusline-inputs.jsonl\"\nprintf '  \\033[32mACTUAL_STATUSLINE\\033[0m  \\n\\n  SECOND_LINE  \\n'\n"
			if err = os.WriteFile(script, []byte(program), 0700); err != nil {
				t.Fatal(err)
			}
			command := "'" + strings.ReplaceAll(script, "'", "'\\''") + "'"
			settings := map[string]any{"statusLine": map[string]any{"type": "command", "command": command, "padding": 2}}
			steps := []map[string]any{{"op": "settle", "milliseconds": 350}}
			if profile == "user-disabled" {
				settings["disableAllHooks"] = true
			} else if profile == "mounted" {
				steps = []map[string]any{{"op": "wait", "count": 1}, {"op": "text", "value": "ACTUAL_STATUSLINE"}, {"op": "state", "value": map[string]any{"mainLoopModel": "statusline-next-model"}}, {"op": "wait", "count": 2}, {"op": "settings", "settings": map[string]any{"statusLine": map[string]any{"type": "command", "command": command, "padding": 4}, "outputStyle": "Explanatory"}}, {"op": "settle", "milliseconds": 350}, {"op": "state", "permissionMode": "plan", "value": map[string]any{}}, {"op": "wait", "count": 3}, {"op": "vim", "value": "NORMAL"}, {"op": "wait", "count": 4}, {"op": "text", "value": "ACTUAL_STATUSLINE"}, {"op": "state", "value": map[string]any{"mainLoopModelForSession": "statusline-session-override"}}, {"op": "wait", "count": 5}}
			}
			input := map[string]any{"home": home, "settings": settings, "trusted": profile != "untrusted", "editorMode": "vim", "steps": steps}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-statusline.fixture.ts")
			sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_STATUSLINE_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=private-statusline-only", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "TZ=UTC"}
			cmd.Stdin = bytes.NewReader(rawMain(input))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal("actual original StatusLine", err, string(output))
			}
			var original struct {
				Records []protocol.JSONObject
				Frames  []protocol.JSONObject
				Spawns  []protocol.JSONObject
				Blocked []string
			}
			if err = json.Unmarshal(output, &original); err != nil || len(original.Blocked) != 0 {
				t.Fatal(err, string(output))
			}
			if profile != "mounted" {
				if len(original.Records) != 0 || len(original.Spawns) != 0 {
					t.Fatal("Source statusline ignored trust/disable gate", string(output))
				}
				if profile == "untrusted" && !strings.Contains(string(output), "statusline-trust-blocked") {
					t.Fatal("original mounted trust notification missing", string(output))
				}
				return
			}
			if len(original.Records) != 5 || len(original.Spawns) != 5 {
				t.Fatal("original trigger/debounce count", string(output))
			}
			first, last := original.Records[0], original.Records[4]
			if objMain(first.Get("model")).String("id") != "fixture-statusline-model" || objMain(last.Get("model")).String("id") != "statusline-session-override" || objMain(last.Get("vim")).String("mode") != "NORMAL" || objMain(last.Get("output_style")).String("name") != "Explanatory" || first.String("cwd") != home || first.String("session_id") == "" || !strings.HasSuffix(first.String("transcript_path"), ".jsonl") {
				t.Fatal("original latest refs/actual callback data", string(output))
			}
			if text := original.Frames[len(original.Frames)-1].String("text"); text != "\x1b[32mACTUAL_STATUSLINE\x1b[0m\nSECOND_LINE" {
				t.Fatal("original stdout whitespace/ANSI result", text)
			}
		})
	}
}
