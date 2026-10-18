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

func TestActualSourceFooterStatusLineShortFullscreenUsesExactRowBoundary(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source required")
	}
	for _, fullscreen := range []bool{false, true} {
		t.Run(map[bool]string{false: "scrollback-short", true: "fullscreen-short"}[fullscreen], func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(home, "statusline-script")
			if err = os.WriteFile(script, []byte("#!/bin/sh\nIFS= read -r input\nprintf '%s\\n' \"$input\" >> \"$HOME/statusline-inputs.jsonl\"\nprintf ORIGINAL_ROW_THRESHOLD\n"), 0700); err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"home": home, "trusted": true, "footer": map[string]any{}, "rows": 23, "settings": map[string]any{"statusLine": map[string]any{"type": "command", "command": "'" + strings.ReplaceAll(script, "'", "'\\''") + "'"}}}
			steps := []map[string]any{{"op": "settle", "milliseconds": 350}}
			if !fullscreen {
				steps = []map[string]any{{"op": "wait", "count": 1}, {"op": "text", "value": "ORIGINAL_ROW_THRESHOLD"}}
			}
			prefix := len(steps)
			steps = append(steps, map[string]any{"op": "footer", "rows": 24}, map[string]any{"op": "wait", "count": 1}, map[string]any{"op": "text", "value": "ORIGINAL_ROW_THRESHOLD"}, map[string]any{"op": "footer", "rows": 23}, map[string]any{"op": "settle", "milliseconds": 350}, map[string]any{"op": "footer", "rows": 24, "value": map[string]any{"suggestions": []any{map[string]any{"name": "private suggestion"}}}})
			if fullscreen {
				steps = append(steps, map[string]any{"op": "wait", "count": 2})
			} else {
				steps = append(steps, map[string]any{"op": "settle", "milliseconds": 350})
			}
			steps = append(steps, map[string]any{"op": "footer", "value": map[string]any{"suggestions": []any{}}}, map[string]any{"op": "wait", "count": 2})
			if !fullscreen {
				// Keep scrollback short throughout. The concrete original Footer
				// should expose this row and unmount only for its suggestions.
				steps = []map[string]any{{"op": "wait", "count": 1}, {"op": "text", "value": "ORIGINAL_ROW_THRESHOLD"}, {"op": "footer", "value": map[string]any{"suggestions": []any{map[string]any{"name": "private suggestion"}}}}, {"op": "settle", "milliseconds": 350}, {"op": "footer", "value": map[string]any{"suggestions": []any{}}}, {"op": "wait", "count": 2}}
			}
			input["steps"] = steps
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-statusline.fixture.ts")
			sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
			command.Dir = home
			flicker := "0"
			if fullscreen {
				flicker = "1"
			}
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_STATUSLINE_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=private-statusline-only", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CLAUDE_CODE_NO_FLICKER=" + flicker}
			command.Stdin = bytes.NewReader(rawMain(input))
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatal("actual original short Footer", err, string(output))
			}
			var original struct {
				Frames          []protocol.JSONObject
				Records, Spawns []protocol.JSONObject
				Blocked         []string
			}
			if err = json.Unmarshal(output, &original); err != nil || len(original.Blocked) != 0 || len(original.Records) != 2 || len(original.Spawns) != 2 {
				t.Fatal(err, string(output))
			}
			count := func(index int) int {
				var rows []protocol.JSONObject
				if err := json.Unmarshal(original.Frames[index].Get("records"), &rows); err != nil {
					t.Fatal(err)
				}
				return len(rows)
			}
			initial := 1
			if fullscreen {
				initial = 0
			}
			matches := false
			if fullscreen {
				matches = count(prefix-1) == initial && count(prefix+4) == 1 && count(prefix+6) == 2
			} else {
				matches = count(1) == 1 && count(3) == 1 && count(5) == 2
			}
			if !matches {
				t.Fatal("original short/suggestion component mount contract", string(output))
			}
			t.Log("actual original Footer 23/24 rows with explicit Source fullscreen preference; fullscreen suggestions preserve mounted StatusLine, scrollback suggestions unmount")
		})
	}
}
