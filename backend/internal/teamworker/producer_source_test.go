package teamworker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestApprovedWorkerFrozenJSONMainThreadToolsAndEffortMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	for _, path := range []string{".claude", "tmp"} {
		_ = os.MkdirAll(filepath.Join(home, path), 0700)
	}
	var fields protocol.JSONObject
	_ = fields.UnmarshalJSON([]byte(`{"agentType":"plugin:worker","source":"plugin","omitClaudeMd":true,"disallowedTools":["Bash(rm *)"],"maxTurns":3,"initialPrompt":"should preserve parser","effort":"low"}`))
	cases := []Start{}
	for _, tools := range []*[]string{nil, func() *[]string { v := []string{"Read", "AskUserQuestion", "Agent", "Read"}; return &v }(), func() *[]string { v := []string{"*"}; return &v }(), func() *[]string { v := []string{}; return &v }()} {
		cases = append(cases, Start{ParentSessionID: "parent", TeamName: "team", MemberID: "member", Name: "worker", SystemPrompt: "  frozen worker prompt  ", Tools: tools, AgentDefinition: fields})
	}
	available := []string{"Read", "Write", "Bash", "AskUserQuestion", "Agent", "SendMessage", "TaskCreate", "TaskGet", "TaskList", "TaskUpdate"}
	rows := []map[string]any{}
	for _, start := range cases {
		sourceStart := map[string]any{"parentSessionId": start.ParentSessionID, "teamName": start.TeamName, "memberId": start.MemberID, "name": start.Name, "systemPrompt": start.SystemPrompt, "agentDefinition": start.AgentDefinition}
		if start.Tools != nil {
			sourceStart["tools"] = *start.Tools
		}
		rows = append(rows, map[string]any{"model": "vendor/precise", "start": sourceStart, "available": available})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-worker-producer.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_WORKER_PRODUCER_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	for i, start := range cases {
		preset, e := Build(start, "vendor/precise", "", false)
		if e != nil {
			t.Fatal(e)
		}
		resolved := agentcatalog.ResolveTools(preset.Definition, available, false, true)
		actual := raw(map[string]any{"fields": preset.Definition.Fields, "agentId": preset.AgentID, "parent": preset.Start.ParentSessionID, "baseTools": preset.CLIBaseTools, "maxTurns": preset.MaxTurns, "tools": resolved.ResolvedTools, "workerEffort": nil, "ordinaryEffort": "high"})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%d %s\n%s", i, actual, source.Rows[i])
		}
		env := Environment(map[string]string{"CLAUDE_CODE_SUBAGENT_MODEL": "parent-subagent", "CLAUDE_CODE_EFFORT_LEVEL": "parent-effort", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN": "1", "PRESERVE": "value"}, start)
		if env["CLAUDE_CODE_EFFORT_LEVEL"] != "" || env["CLAUDE_CODE_SUBAGENT_MODEL"] != "" || env["CLAUDE_CODE_RESUME_INTERRUPTED_TURN"] != "" || env["PRESERVE"] != "value" || InheritPersistedEffort(env) {
			t.Fatal(env)
		}
	}
}
