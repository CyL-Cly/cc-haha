package agentcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestResolvedAgentToolRulesMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	available := []string{"Agent", "AskUserQuestion", "Read", "Write", "Bash", "Glob", "Grep", "Skill", "ExitPlanMode", "EnterPlanMode", "TaskOutput", "TaskStop", "Workflow", "TaskCreate", "TaskUpdate", "EnterWorktree", "ExitWorktree", "mcp__fixture__real"}
	declarations := []string{`{}`, `{"tools":[]}`, `{"tools":["*"]}`, `{"tools":["Read","Bash(git status, log)","Read","Agent(worker, researcher)","Write","Unknown"]}`, `{"tools":["Task(worker)","KillShell","BashOutputTool","AgentOutputTool","Grep"],"disallowedTools":["Read","Write(secret)","Grep(*)"]}`, `{"tools":["*","Read"]}`, `{"permissionMode":"plan","tools":["ExitPlanMode","AskUserQuestion","EnterWorktree","TaskCreate","mcp__fixture__real"]}`, `{"disallowedTools":["Agent(worker)","Task(*)","mcp__fixture__real"]}`}
	type scenario struct {
		Definition protocol.JSONObject `json:"definition"`
		Available  []string            `json:"available"`
		Async      bool                `json:"async"`
		Main       bool                `json:"main"`
	}
	cases := []scenario{}
	actual := []json.RawMessage{}
	for _, raw := range declarations {
		for _, mode := range []struct{ async, main bool }{{false, false}, {true, false}, {false, true}} {
			var fields protocol.JSONObject
			_ = fields.UnmarshalJSON([]byte(raw))
			fields.Set("source", []byte(`"userSettings"`))
			row := scenario{fields, available, mode.async, mode.main}
			cases = append(cases, row)
			result := ResolveTools(Definition{Source: "userSettings", Fields: fields}, available, mode.async, mode.main)
			data, _ := json.Marshal(result)
			actual = append(actual, protocol.StringifyValidatedJavaScriptJSON(data))
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-definition.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_AGENT_DEFINITION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "resolve": cases})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source tool rules %v %.2000s", err, stderr.String())
	}
	var source struct {
		Resolved     []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(output, &source) != nil || len(source.Resolved) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source invalid/escaped %.2000s", output)
	}
	for i, want := range source.Resolved {
		if string(protocol.StringifyValidatedJavaScriptJSON(want)) != string(actual[i]) {
			t.Errorf("case%d Go %s Source %s", i, actual[i], want)
		}
	}
}
