package teamworker

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSupervisorInstructionsDependenciesAndFailuresMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	plan := launchPlan("helper-source", home)
	approved := object(plan.Get("approvedSnapshot"))
	members := rows(approved.Get("members"))
	proposal := rows(approved.Get("tasks"))
	mapping := map[string]string{"proposal-a": "31", "proposal-b": "32"}
	failures := []string{"", "network timeout 503", "authentication 401 network", "stream_truncated: ended without message_stop", "billing 402", "interrupted by user", "invalid_request_error timeout", "maximum context window 429", "rate limit 429", "arbitrary failure", "\uFEFF\n\t network reset\uFEFF", "\u0000bad gateway\u0008\nignored", "emoji " + strings.Repeat("😀", 150)}
	tasks := []protocol.JSONObject{validationObject(map[string]any{"id": "31", "subject": "first", "owner": "worker-one", "status": "pending", "blockedBy": []string{}}), validationObject(map[string]any{"id": "32", "subject": "next", "owner": "worker-two", "status": "pending", "blockedBy": []string{"31", "missing"}})}
	inputs, expected := []any{}, []any{}
	for _, status := range []string{"pending", "completed"} {
		tasks[0].Set("status", raw(status))
		for _, member := range members {
			inputs = append(inputs, raw(map[string]any{"member": member, "members": members, "proposalTasks": proposal, "mapping": mapping, "tasks": append([]protocol.JSONObject{}, tasks...), "failures": failures}))
			classification := []any{}
			for _, text := range failures {
				reason := firstLine(text)
				classification = append(classification, map[string]any{"transient": IsTransientTurnFailure(text), "reason": reason, "capped": capText(text, 200), "retry": FormatTeammateAutoContinuePrompt(reason, 1, 5)})
			}
			expected = append(expected, raw(map[string]any{"brief": memberBrief(member, members, proposal, mapping), "ready": readyTasksOf(member.String("name"), tasks), "waiting": waitsForDependencies(member.String("name"), tasks), "failures": classification}))
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-supervisor-helpers.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_SUPERVISOR_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "rows": inputs}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var report struct {
		Results      json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &report); e != nil || len(report.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(output))
	}
	var a, b any
	_ = json.Unmarshal(raw(expected), &a)
	_ = json.Unmarshal(report.Results, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go%s\nSource%s", raw(a), report.Results)
	}
}
