package trajectory

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTrajectoryProjectionAgainstFrozenMainSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("explicit pinned Source Bun unavailable")
	}
	root := os.Getenv("CC_HAHA_TRAJECTORY_SOURCE_ROOT")
	if root == "" {
		root, _ = filepath.Abs("../../..")
	}
	fixtures := []Object{prompt("u01", "first prompt"), {"type": "user", "uuid": "skill", "isMeta": true, "message": Object{"role": "user", "content": "Base directory for this skill: /skill\nbody"}}, {"type": "assistant", "uuid": "a01", "timestamp": "2026-10-12T00:00:02.000Z", "requestId": "request", "message": Object{"id": "message", "model": "fixture", "content": []any{Object{"type": "thinking", "thinking": "secret"}}, "usage": Object{"input_tokens": 3, "output_tokens": 0}}}, {"type": "assistant", "uuid": "a02", "timestamp": "2026-10-12T00:00:03.000Z", "message": Object{"id": "message", "content": []any{Object{"type": "text", "text": "## Title\n- **answer**"}, Object{"type": "tool_use", "id": "tool", "name": "Bash", "input": Object{"command": "echo hi"}}}, "usage": Object{"input_tokens": 3, "output_tokens": 7}, "stop_reason": "tool_use"}}, {"type": "user", "uuid": "r", "timestamp": "2026-10-12T00:00:04.000Z", "message": Object{"role": "user", "content": []any{Object{"type": "tool_result", "tool_use_id": "tool", "content": []any{Object{"type": "text", "text": "image result"}, Object{"type": "image", "source": Object{}}}}}}}, {"type": "user", "uuid": "delivered", "isMeta": true, "message": Object{"role": "user", "content": collaborationPrefix + `{"senderSessionId":"peer","messageId":"mail","text":"work result"}`}}, {"type": "system", "uuid": "api", "subtype": "api_error", "level": "error", "retryAttempt": 2., "maxRetries": 10., "retryInMs": 1182.6, "error": Object{"status": 503., "error": Object{"error": Object{"message": "No available channel"}}}}, {"type": "system", "uuid": "summary", "subtype": "stop_hook_summary", "hookCount": 2., "hookInfos": []any{Object{"command": "/scripts/on-stop.sh", "durationMs": 379.}, Object{"command": "lint", "durationMs": 1500.}}, "hookErrors": []any{"lint failed"}, "preventedContinuation": true, "stopReason": "fix lint", "totalDurationMs": 1879.}, {"type": "system", "uuid": "save", "subtype": "memory_saved", "writtenPaths": []any{"/m/MEMORY.md", "/m/note.md"}}, {"type": "system", "uuid": "future", "subtype": "future", "level": "error", "detail": "new message"}, {"type": "user", "uuid": "side", "isSidechain": true, "message": Object{"role": "user", "content": "side prompt"}}}
	for _, attachment := range []Object{{"type": "invoked_skills", "skills": []any{Object{"name": "pdf"}, Object{"name": "xlsx"}}}, {"type": "todo_reminder", "itemCount": 3.}, {"type": "hook_success", "hookName": "PostToolUse:Bash", "hookEvent": "PostToolUse", "exitCode": 0.}, {"type": "deferred_tools_record", "entries": []any{Object{"name": "Read"}, Object{"name": "Write"}}}, {"type": "environment", "snapshot": Object{"workingDirectory": "/w", "platform": "darwin", "shell": "zsh", "osVersion": "Darwin 25"}, "changes": []any{Object{"field": "workingDirectory"}}}, {"type": "queued_command", "prompt": "extra steering", "commandMode": "prompt"}, {"type": "brand_new_type", "detail": "first string field wins"}} {
		fixtures = append(fixtures, Object{"type": "attachment", "uuid": "att" + text(attachment["type"]), "timestamp": "2026-10-12T00:00:05.000Z", "attachment": attachment})
	}
	// Round-trip to represent the same JSON number/property values as Source.
	var normalized []Object
	json.Unmarshal(raw(fixtures), &normalized)
	entries := []Entry{}
	sourceEntries := []Object{}
	offset := int64(4096)
	for _, v := range normalized {
		end := offset + int64(len(raw(v)))
		entries = append(entries, Entry{v, offset, end, 0})
		sourceEntries = append(sourceEntries, Object{"entry": v, "byteStart": offset, "byteEnd": end})
		offset = end + 1
	}
	base := 4
	cases := []Object{}
	actual := [][]Row{}
	for _, main := range []bool{true, false} {
		cases = append(cases, Object{"entries": sourceEntries, "options": Object{"turnBase": base, "skipSidechain": main, "precedingTimestamps": []string{"2026-10-12T00:00:00Z"}}})
		actual = append(actual, Project(entries, &base, main, []string{"2026-10-12T00:00:00Z"}))
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", "testdata/source.fixture.ts")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "NODE_ENV=test", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "CC_HAHA_TRAJECTORY_SOURCE_ROOT=" + root}
	cmd.Stdin = bytes.NewReader(raw(Object{"cases": cases}))
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual frozen MAIN Source: %v %s", e, out)
	}
	var expected any
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "TRAJECTORY_SOURCE_RESULT:") {
			json.Unmarshal([]byte(strings.TrimPrefix(line, "TRAJECTORY_SOURCE_RESULT:")), &expected)
		}
	}
	var got any
	json.Unmarshal(raw(actual), &got)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("native projection differs from actual MAIN\nGo: %s\nSource: %s", raw(got), raw(expected))
	}
}
