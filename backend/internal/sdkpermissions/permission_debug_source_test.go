package sdkpermissions

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourcePermissionDebugShadowingAndDisplayConsumers(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	sources := []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings", "cliArg", "command", "session"}
	modes := []string{"default", "plan", "acceptEdits", "bypassPermissions", "dontAsk", "auto", "unknown"}
	ruleValues := []map[string]string{{"toolName": "Bash"}, {"toolName": "Bash", "ruleContent": "npm install"}, {"toolName": "PowerShell", "ruleContent": "Write-Output \"中文🙂(x)\" C:\\Owned\\path"}, {"toolName": "Bash", "ruleContent": ""}}
	type row struct {
		Context          protocol.JSONObject `json:"context"`
		SandboxAutoAllow bool                `json:"sandboxAutoAllow"`
		RuleValues       []map[string]string `json:"ruleValues"`
		Sources          []string            `json:"sources"`
		Modes            []string            `json:"modes"`
	}
	rows := []row{}
	for _, allow := range sources {
		for _, other := range sources {
			for _, kind := range []string{"alwaysAskRules", "alwaysDenyRules", "both"} {
				for _, sandbox := range []bool{false, true} {
					context := protocol.NewJSONObject()
					allowRules, _ := json.Marshal(map[string][]string{allow: {"Bash(npm:*)", "Bash", "PowerShell(Write-Output:*)", "Agent(work:*)", "Bash(*)", "Bash()"}})
					context.Set("alwaysAllowRules", allowRules)
					for _, key := range []string{"alwaysAskRules", "alwaysDenyRules"} {
						value := map[string][]string{}
						if kind == key || kind == "both" {
							value[other] = []string{"Bash", "PowerShell(*)", "Task", "Bash(unrelated:*)"}
							value["unknown-source"] = []string{"Bash"}
						}
						data, _ := json.Marshal(value)
						context.Set(key, data)
					}
					rows = append(rows, row{context, sandbox, ruleValues, sources, modes})
				}
			}
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-permission-debug.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PERMISSION_DEBUG_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"rows": rows})
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal("actual permission debug Source", err, stderr.String())
	}
	var original []struct {
		Unreachable []struct {
			Source, Behavior, Reason, Fix, ShadowType string
			Rule                                      protocol.JSONObject
		}
		RuleDisplays, SourceDisplays, ModeDisplays []string
	}
	if err = json.Unmarshal(output, &original); err != nil || len(original) != len(rows) {
		t.Fatal("actual permission debug Source receipts", err, len(original), len(rows), "bytes", len(output), "tail", string(output[max(0, len(output)-120):]))
	}
	for i, input := range rows {
		before, _ := input.Context.MarshalJSON()
		actual := DetectUnreachablePermissionRules(input.Context, input.SandboxAutoAllow)
		want := original[i]
		if len(actual) != len(want.Unreachable) {
			t.Fatalf("Source shadow count row%d: Go%d Source%d", i, len(actual), len(want.Unreachable))
		}
		for j, value := range actual {
			expected := want.Unreachable[j]
			actualRule, _ := value.Rule.MarshalJSON()
			expectedRule, _ := expected.Rule.MarshalJSON()
			var actualJSON, expectedJSON any
			_ = json.Unmarshal(actualRule, &actualJSON)
			_ = json.Unmarshal(expectedRule, &expectedJSON)
			if value.Source != expected.Source || value.Behavior != expected.Behavior || value.Reason != expected.Reason || value.Fix != expected.Fix || value.ShadowType != expected.ShadowType || !reflect.DeepEqual(actualJSON, expectedJSON) {
				t.Fatalf("Source shadow rule row%d/%d differs: Go%+v Source%+v", i, j, value, expected)
			}
		}
		for j, rule := range input.RuleValues {
			data, _ := json.Marshal(rule)
			var parsed protocol.JSONObject
			_ = parsed.UnmarshalJSON(data)
			if PermissionRuleDisplay(parsed) != want.RuleDisplays[j] {
				t.Fatal("actual rule display", i, j, PermissionRuleDisplay(parsed), want.RuleDisplays[j])
			}
		}
		for j, source := range sources {
			if PermissionRuleSourceDisplay(source) != want.SourceDisplays[j] {
				t.Fatal("actual setting source display", i, j)
			}
		}
		for j, mode := range modes {
			if PermissionModeDisplay(mode) != want.ModeDisplays[j] {
				t.Fatal("actual permission mode display", i, j)
			}
		}
		after, _ := input.Context.MarshalJSON()
		if !bytes.Equal(before, after) {
			t.Fatal("debug display mutated policy", i)
		}
	}
}
