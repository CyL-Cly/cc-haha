package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeTeamPresetLifecycleMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	names := []string{"Read", "Write", "AskUserQuestion", "Agent", "SendMessage", "TaskCreate", "TaskGet", "TaskList", "TaskUpdate"}
	start := teamworker.Start{ParentSessionID: "parent", TeamName: "MyTeam_", MemberID: "m", Name: "worker", SystemPrompt: "ACTUAL_APPROVED_WORKER_SYSTEM", Tools: &names, AgentDefinition: protocol.NewJSONObject()}
	m, f, _ := teamWorkerFixture(t, start, "http://127.0.0.1:1")
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "disabled", "SKILL.md"), "---\ndescription: Private manual-only skill\ndisable-model-invocation: true\n---\nTHIS_DISABLED_SKILL_MUST_NOT_PRELOAD")
	rows := []map[string]any{
		{"definition": map[string]any{"tools": names, "skills": []string{"prime"}}, "available": names},
		{"definition": map[string]any{"tools": []string{"Read", "Agent(general-purpose)", "AskUserQuestion"}, "disallowedTools": []string{"Read(/secret)"}}, "available": names},
		{"definition": map[string]any{"tools": []string{}}, "available": names},
		{"definition": map[string]any{"tools": []string{"*"}, "skills": []string{"missing"}}, "available": names},
		{"definition": map[string]any{"tools": names, "mcpServers": []string{"missing"}}, "available": names},
		{"definition": map[string]any{"tools": names, "mcpServers": []any{map[string]any{"inline": map[string]any{"command": "false"}}}}, "available": names},
		{"definition": map[string]any{"tools": names, "hooks": map[string]any{"Stop": []any{}}}, "available": names},
		{"definition": map[string]any{"tools": names, "skills": []string{"disabled"}}, "available": names},
		{"definition": map[string]any{"tools": names, "skills": []string{"help"}}, "available": names},
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-worker-preset.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_TEAM_WORKER_PRESET_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=true", "ANTHROPIC_API_KEY=private-preset-fixture", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": f.home, "cwd": f.cwd, "cases": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	if err = m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		var actual json.RawMessage
		err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			definition := protocol.NewJSONObject()
			_ = definition.UnmarshalJSON(rawMain(row["definition"]))
			definition.Set("description", quoteMain("Approved team member"))
			definition.Set("prompt", quoteMain("ACTUAL_APPROVED_WORKER_SYSTEM"))
			definition.Set("model", quoteMain("fixture-team-model"))
			parsed, err := agentcatalog.ParseJSONDefinition("worker", rawMain(definition), "flagSettings", "external", false)
			if err != nil {
				return err
			}
			m.teamWorker.preset.Definition = *parsed
			prepared, failure := m.prepareNativeTeamWorkerPreset(context.Background(), scope, names)
			messages := []protocol.JSONObject{}
			for _, message := range prepared.Messages {
				messages = append(messages, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: message.Get("type")}, protocol.JSONField{Name: "message", Value: message.Get("message")}, protocol.JSONField{Name: "isMeta", Value: message.Get("isMeta")}))
			}
			var errorText any
			if failure != nil {
				errorText = failure.Error()
			}
			tools := prepared.Tools
			if tools == nil {
				tools = []string{}
			}
			list, err := m.Controller.TaskListIDOnOwner(scope, "worker@MyTeam_")
			if err != nil {
				return err
			}
			actual = rawMain(map[string]any{"tools": tools, "messages": messages, "error": errorText, "taskList": list})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, actual, source.Rows[i])
		}
	}
}
