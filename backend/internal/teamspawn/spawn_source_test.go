package teamspawn

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInProcessSpawnTaskStateNamesModelAndPhysicalRosterAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	rows := []map[string]any{}
	for i, input := range []map[string]any{
		{"name": "worker", "prompt": "actual initial prompt", "agent_type": "general-purpose", "description": "initial"},
		{"name": "Worker", "prompt": "different prompt", "description": "duplicate names"},
		{"name": "worker", "prompt": strings.Repeat("a", 49) + "😀 tail", "model": "sonnet", "description": "alias"},
		{"name": "some@worker", "prompt": "original separator replacement", "model": "inherit"},
		{"name": "worker", "prompt": "required mode", "plan_mode_required": true},
		{"name": "worker", "prompt": "custom declaration", "agent_type": "custom"},
	} {
		team := map[string]any{"name": "actual-spawn-" + string(rune('a'+i)), "leadSessionId": "11111111-1111-4111-8111-111111111111", "createdAt": 1700000000000, "members": []map[string]any{{"name": "worker", "agentId": "worker@team"}, {"name": "WORKER-2", "agentId": "two@team"}}}
		input["team_name"] = team["name"]
		row := map[string]any{"team": team, "input": input, "leaderModel": "fixture-leader", "toolUseId": "actual-spawn-use"}
		if i == 5 {
			row["definitions"] = []map[string]any{{"agentType": "custom", "source": "projectSettings", "model": "haiku", "tools": []string{"Read"}, "rawSystemPrompt": "actual custom body"}}
		}
		rows = append(rows, row)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-spawn.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CLAUDE_CODE_TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INPROCESS_TEAM_SPAWN_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=fake-private", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "ANTHROPIC_DEFAULT_SONNET_MODEL=fixture-sonnet", "ANTHROPIC_DEFAULT_HAIKU_MODEL=fixture-haiku", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": home, "cwd": home, "sessionId": "11111111-1111-4111-8111-111111111111", "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var report struct {
		Rows         []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &report); err != nil || len(report.Capabilities.Blocked) > 0 || len(report.Rows) != len(rows) {
		t.Fatal(err, string(output), stderr.String())
	}
	for i, row := range report.Rows {
		original, _ := row.Object("task")
		input := inboxObject(rawJSON(rows[i]["input"]))
		team := inboxObject(rawJSON(rows[i]["team"]))
		var members []protocol.JSONObject
		_ = json.Unmarshal(team.Get("members"), &members)
		name := strings.ReplaceAll(UniqueName(input.String("name"), members), "@", "-")
		model, err := ResolveModel("", input.String("model"), map[bool]string{true: "haiku"}[i == 5], "fixture-leader", i == 5, nil, func(value string) (string, error) {
			if value == "sonnet" {
				return "fixture-sonnet", nil
			}
			if value == "haiku" {
				return "fixture-haiku", nil
			}
			return value, nil
		}, func() (string, error) { t.Error("unexpected fallback"); return "", nil })
		if err != nil {
			t.Fatal(err)
		}
		var now float64
		_ = json.Unmarshal(original.Get("startTime"), &now)
		expected, err := CreateState(StateOptions{ID: original.String("id"), Name: name, Team: team.String("name"), Prompt: input.String("prompt"), ParentSessionID: "11111111-1111-4111-8111-111111111111", Color: original.String("identityColor"), OutputFile: original.String("outputFile"), Model: quoted(model), ToolUseID: quoted("actual-spawn-use"), SpinnerVerb: original.Get("spinnerVerb"), PastTenseVerb: original.Get("pastTenseVerb"), PlanRequired: string(input.Get("plan_mode_required")) == "true", Now: now})
		if err != nil {
			t.Fatal(err)
		}
		identity, _ := original.Object("identity")
		nativeIdentity, _ := expected.Object("identity")
		nativeIdentity.Set("color", identity.Get("color"))
		expected.Set("identity", rawJSON(nativeIdentity))
		var actual, want any
		_ = json.Unmarshal(rawJSON(expected), &actual)
		_ = json.Unmarshal(rawJSON(original), &want)
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("case%d Go %s\nSource%s", i, rawJSON(expected), rawJSON(original))
		}
		launch, _ := row.Object("launch")
		var pinned []any
		_ = json.Unmarshal(launch.Get("messages"), &pinned)
		if len(pinned) != 0 {
			t.Fatal("Source spawn pinned parent conversation")
		}
		if len(original.String("id")) != 9 || original.String("id")[0] != 't' {
			t.Fatal("original task ID", original)
		}
	}
	verbs, err := SpinnerVerbs(protocol.NewJSONObject())
	if err != nil || len(verbs) < 100 {
		t.Fatal("actual spinner palette", err, len(verbs))
	}
}
