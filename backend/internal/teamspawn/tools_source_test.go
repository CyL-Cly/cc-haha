package teamspawn

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInProcessTeammateActualAsyncToolFilteringAgainstSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	pool := []string{"Read", "Write", "Bash", "AskUserQuestion", "Agent", "TeamCreate", "TeamDelete", "TeamPlan", "TaskCreate", "TaskGet", "TaskList", "TaskUpdate", "SendMessage", "ExitPlanMode", "EnterPlanMode", "Workflow", "TaskStop", "TaskOutput", "Skill", "ToolSearch", "EnterWorktree", "ExitWorktree", "mcp__actual__call"}
	rows, actual := []map[string]any{}, []agentcatalog.ResolvedTools{}
	for _, body := range []string{`{}`, `{"tools":[]}`, `{"tools":["*"]}`, `{"tools":["TaskCreate","Read","TeamCreate","TeamDelete","SendMessage","TaskUpdate"]}`, `{"tools":["Agent(Explore)","AskUserQuestion","Workflow","TaskStop"]}`, `{"disallowedTools":["TaskUpdate","Read","mcp__actual__call"]}`, `{"permissionMode":"plan"}`, `{"permissionMode":"plan","tools":["ExitPlanMode","EnterPlanMode","TaskList"]}`} {
		var fields protocol.JSONObject
		if err := fields.UnmarshalJSON([]byte(body)); err != nil {
			t.Fatal(err)
		}
		fields.Set("source", quoted("projectSettings"))
		rows = append(rows, map[string]any{"definition": fields, "available": pool})
		actual = append(actual, agentcatalog.ResolveInProcessTools(agentcatalog.Definition{Source: "projectSettings", Fields: fields}, pool, false))
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-tools.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INPROCESS_TEAM_TOOLS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var source struct {
		Rows         json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	want, _ := json.Marshal(actual)
	var a, b any
	_ = json.Unmarshal(want, &a)
	_ = json.Unmarshal(source.Rows, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go %s\nSource %s", want, source.Rows)
	}
}
