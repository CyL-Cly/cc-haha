package attachments_test

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

	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceAllPlanAttachmentWorkflowsCountsAndUTF16(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	type row struct {
		payload string
		options attachments.PlanOptions
	}
	rows := []row{}
	base := attachments.PlanOptions{Environment: map[string]string{}, ExplorePlanAgentsEnabled: true}
	for _, payload := range []string{`{"type":"plan_mode","reminderType":"full","planFilePath":"/private/plan.md","planExists":false}`, `{"type":"plan_mode","reminderType":"full","planFilePath":"literal __PHASE4__ \ud800","planExists":true}`, `{"type":"plan_mode","reminderType":"sparse","planFilePath":null}`, `{"type":"plan_mode","isSubAgent":true,"reminderType":"sparse","planFilePath":"/p","planExists":true}`, `{"type":"plan_mode","isSubAgent":true}`, `{"type":"plan_mode_reentry","planFilePath":"/p"}`, `{"type":"plan_mode_exit","planExists":false}`, `{"type":"plan_mode_exit","planExists":"truthy","planFilePath":"\ud800"}`} {
		rows = append(rows, row{payload, base})
	}
	for _, variant := range []string{"trim", "cut", "cap", "unexpected"} {
		o := base
		o.Phase4Variant = variant
		rows = append(rows, row{rows[0].payload, o})
	}
	for _, count := range []string{"2", "10", "11", "0", "-1", "+3rest", "  \ufeff4.5", "1e2", "0x10", "not number", "00000000000000000000000000002", "9999999999999999999999999999"} {
		o := base
		o.Environment = map[string]string{"CLAUDE_CODE_PLAN_V2_AGENT_COUNT": count, "CLAUDE_CODE_PLAN_V2_EXPLORE_AGENT_COUNT": count}
		rows = append(rows, row{rows[0].payload, o})
	}
	for _, subscription := range []string{"team", "enterprise", "max", "pro", ""} {
		o := base
		o.SubscriptionType = subscription
		o.RateLimitTier = "default_claude_max_20x"
		rows = append(rows, row{rows[0].payload, o})
	}
	for _, allowed := range [][]string{nil, {}, {"Read"}, {"Grep", "Read"}, {"Bash"}} {
		o := base
		o.InterviewPhase = true
		o.ProjectAllowedTools = allowed
		rows = append(rows, row{rows[0].payload, o})
	}
	o := base
	o.InterviewPhase = true
	o.EmbeddedSearchTools = true
	o.ProjectAllowedTools = []string{"Bash"}
	rows = append(rows, row{rows[0].payload, o})
	o.EmbeddedSearchTools = false
	o.ExplorePlanAgentsEnabled = false
	o.Environment = map[string]string{"USER_TYPE": "ant"}
	rows = append(rows, row{rows[0].payload, o})
	o = base
	o.InterviewPhase = true
	rows = append(rows, row{rows[2].payload, o})
	inputs := []map[string]any{}
	for _, row := range rows {
		inputs = append(inputs, map[string]any{"payload": row.payload, "options": map[string]any{"environment": row.options.Environment, "subscriptionType": row.options.SubscriptionType, "rateLimitTier": row.options.RateLimitTier, "interviewPhase": row.options.InterviewPhase, "phase4Variant": row.options.Phase4Variant, "embeddedSearchTools": row.options.EmbeddedSearchTools, "exploreAgents": row.options.ExplorePlanAgentsEnabled, "allowedTools": row.options.ProjectAllowedTools}})
	}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "inputs": []any{}, "attachments": inputs})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plan.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_PLAN_FIXTURE=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(data)
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("Source: %v %.3000s", e, output)
	}
	var source struct {
		Observations []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil {
		t.Fatalf("Source JSON %v %.3000s", e, output)
	}
	if len(source.Observations) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Source cases/capabilities", len(source.Observations), source.Capabilities)
	}
	for i, row := range rows {
		factory, _ := messages.NewFactory(func() string { return "original-shared-factory-id" }, func() time.Time { return time.Unix(1700000000, 0) })
		out, e := attachments.ProjectWithOptions(json.RawMessage(row.payload), factory, &row.options)
		if e != nil {
			t.Fatal(i, e)
		}
		actual := []protocol.JSONObject{}
		for _, message := range out {
			actual = append(actual, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: message.Get("type")}, protocol.JSONField{Name: "message", Value: message.Get("message")}, protocol.JSONField{Name: "isMeta", Value: message.Get("isMeta")}))
		}
		got, _ := json.Marshal(actual)
		var g, w any
		_ = json.Unmarshal(got, &g)
		_ = json.Unmarshal(source.Observations[i], &w)
		if !reflect.DeepEqual(g, w) {
			t.Errorf("case%d Source=%s\nGo=%s", i, source.Observations[i], got)
		}
	}
}
