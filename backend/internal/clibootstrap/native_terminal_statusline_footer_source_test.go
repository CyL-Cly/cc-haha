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

func TestActualSourceFooterStatusLineUnmountRemountRetainsTextAndCancelsHeldCommand(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned original Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{".claude", "tmp"} {
		if err = os.MkdirAll(filepath.Join(home, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	script, held := filepath.Join(home, "statusline-script"), filepath.Join(home, "statusline-held")
	if err = os.WriteFile(script, []byte("#!/bin/sh\nIFS= read -r input\nprintf '%s\\n' \"$input\" >> \"$HOME/statusline-inputs.jsonl\"\nprintf ORIGINAL_FOOTER_TEXT\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(held, []byte("#!/usr/bin/python3\nimport os,sys,time\nwith open(os.environ['HOME']+'/statusline-inputs.jsonl','a') as f: f.write(sys.stdin.readline())\ntime.sleep(60)\nwith open(os.environ['HOME']+'/late-statusline','w') as f: f.write('late')\n"), 0700); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	normal := map[string]any{"statusLine": map[string]any{"type": "command", "command": "exec " + quote(script)}}
	hold := map[string]any{"statusLine": map[string]any{"type": "command", "command": "exec " + quote(held)}}
	steps := []map[string]any{{"op": "wait", "count": 1}, {"op": "text", "value": "ORIGINAL_FOOTER_TEXT"}, {"op": "footer", "value": map[string]any{"mode": "bash"}}, {"op": "settle", "milliseconds": 350}, {"op": "footer", "value": map[string]any{"mode": "prompt"}}, {"op": "wait", "count": 2}, {"op": "settings", "settings": hold}, {"op": "wait", "count": 3}, {"op": "footer", "value": map[string]any{"helpOpen": true}}, {"op": "child-retired", "index": 2}, {"op": "settings", "settings": normal}, {"op": "settle", "milliseconds": 350}, {"op": "footer", "value": map[string]any{"helpOpen": false}}, {"op": "wait", "count": 4}, {"op": "footer", "value": map[string]any{"isPasting": true}}, {"op": "settle", "milliseconds": 350}, {"op": "footer", "value": map[string]any{"isPasting": false, "exitMessage": map[string]any{"show": true}}}, {"op": "settle", "milliseconds": 350}, {"op": "footer", "value": map[string]any{"exitMessage": map[string]any{"show": false}, "suggestions": []any{map[string]any{"name": "private suggestion"}}}}, {"op": "settle", "milliseconds": 350}, {"op": "footer", "value": map[string]any{"suggestions": []any{}}}, {"op": "wait", "count": 5}}
	input := map[string]any{"home": home, "settings": normal, "trusted": true, "footer": map[string]any{}, "steps": steps}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-statusline.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_STATUSLINE_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=private-only", "ANTHROPIC_BASE_URL=http://127.0.0.1:1"}
	command.Stdin = bytes.NewReader(rawMain(input))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("whole original Footer StatusLine lifetime", err, string(output))
	}
	var original struct {
		Frames          []protocol.JSONObject
		Records, Spawns []protocol.JSONObject
		Blocked         []string
	}
	if err = json.Unmarshal(output, &original); err != nil || len(original.Blocked) != 0 || len(original.Records) != 5 || len(original.Spawns) != 5 {
		t.Fatal(err, string(output))
	}
	for _, frame := range original.Frames {
		if frame.String("text") != "ORIGINAL_FOOTER_TEXT" && frame.String("op") != "wait" {
			t.Fatal("Source AppState text discarded on component unmount", string(output))
		}
	}
	if _, err = os.Stat(filepath.Join(home, "late-statusline")); !os.IsNotExist(err) {
		t.Fatal("cancelled original held shell had late effect", err)
	}
	t.Logf("actual original Footer normal/bash/help/paste/exit/suggestions remounts=5, held child reaped, blocked=%d", len(original.Blocked))
}
