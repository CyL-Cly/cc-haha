package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestAgentWorktreeResultMappingMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	cases := []protocol.JSONObject{}
	for _, fields := range [][]protocol.JSONField{
		{field("worktreePath", "/private/agent-worktree"), field("worktreeBranch", "worktree-agent-a1234567")},
		{field("worktreePath", "/private/hook-worktree")},
		{field("worktreeIsolationSkipped", "the workspace is not a git repository and no WorktreeCreate hook is configured")},
		{field("agentType", "Explore"), field("worktreePath", "/private/agent-worktree"), field("worktreeBranch", "worktree-agent-a1234567")},
		{field("agentType", "Explore")},
	} {
		row := input(`{"status":"completed","prompt":"actual task","agentId":"actual-child","agentType":"general-purpose","content":[{"type":"text","text":"child report"}],"totalTokens":3,"totalToolUseCount":1,"totalDurationMs":12}`)
		for _, f := range fields {
			row.Set(f.Name, f.Value)
		}
		cases = append(cases, row)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-worktree-result.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AGENT_WORKTREE_RESULT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cases": cases})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Capabilities.Blocked) > 0 || len(source.Rows) != len(cases) {
		t.Fatal("invalid Source", e, string(output))
	}
	for i, row := range cases {
		mapped, e := mapAgent(row, quote("original-agent-use"))
		if e != nil {
			t.Fatal(e)
		}
		if string(protocol.StringifyValidatedJavaScriptJSON(raw(mapped))) != string(protocol.StringifyValidatedJavaScriptJSON(source.Rows[i])) {
			t.Errorf("map%d Source%s Go%s", i, source.Rows[i], raw(mapped))
		}
	}
}
