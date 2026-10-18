package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func powerShellDecisionProjection(decision queryloop.ToolPermission) protocol.JSONObject {
	value := protocol.NewJSONObject(field("behavior", decision.Behavior))
	for _, row := range []struct {
		Name  string
		Value json.RawMessage
	}{{"message", decision.Message}, {"decisionReason", decision.DecisionReason}, {"suggestions", decision.Suggestions}} {
		if row.Value != nil {
			value.Set(row.Name, row.Value)
		}
	}
	if decision.UpdatedInput != nil {
		data, _ := decision.UpdatedInput.MarshalJSON()
		value.Set("updatedInput", data)
	}
	var metadata protocol.JSONObject
	if metadata.UnmarshalJSON(decision.Metadata) == nil && metadata.Get("blockedPath") != nil {
		value.Set("blockedPath", metadata.Get("blockedPath"))
	}
	return value
}
func TestActualSourcePowerShellOuterPermissionNormalizationAndBypassDenialPrecedence(t *testing.T) {
	executable := physicalPowerShell(t)
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	if _, err := os.Stat(executable); err != nil {
		t.Skip("physical PowerShell AST required")
	}
	s, owner, home, permission := fixture(t)
	if err := os.Mkdir(filepath.Join(home, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".git", "HEAD"), []byte("ref: refs/heads/private"), 0600); err != nil {
		t.Fatal(err)
	}
	s.options.Environment["CLAUDE_CODE_POWERSHELL_PATH"] = executable
	s.options.Environment["TMPDIR"] = home
	s.options.Shells = windowshell.NewNative("darwin", home, s.options.Environment)
	s.options.Shells.Warm(context.Background())
	contexts := []map[string]any{{"mode": "default"}, {"mode": "acceptEdits"}, {"mode": "bypassPermissions"}, {"mode": "plan", "isBypassPermissionsModeAvailable": true}, {"mode": "plan", "isBypassPermissionsModeAvailable": false}, {"mode": "dontAsk"}, {"mode": "default", "alwaysAllowRules": map[string][]string{"localSettings": {"PowerShell"}}}, {"mode": "default", "alwaysAskRules": map[string][]string{"localSettings": {"PowerShell"}}}, {"mode": "default", "alwaysDenyRules": map[string][]string{"localSettings": {"PowerShell"}}}, {"mode": "default", "alwaysAllowRules": map[string][]string{"localSettings": {"PowerShell(*)"}}}, {"mode": "bypassPermissions", "alwaysDenyRules": map[string][]string{"localSettings": {"Edit(private.txt)"}}}, {"mode": "default", "alwaysAllowRules": map[string][]string{"localSettings": {"PowerShell"}}, "alwaysDenyRules": map[string][]string{"localSettings": {"Edit(private.txt)"}}}}
	commands := []string{`Get-Process`, `touch private.txt`, `Remove-Item /`, `Set-Content private.txt value`, `Set-Content .git/config value`, `Get-Process; Remove-Item /`}
	rows := []map[string]any{}
	for _, command := range commands {
		for _, context := range contexts {
			rows = append(rows, map[string]any{"context": context, "input": map[string]any{"command": command}})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-powershell-policy.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_TMPDIR=" + home, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_POWERSHELL_POLICY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "executable": executable, "normalizedEvaluations": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		NormalizedEvaluations []struct{ Result json.RawMessage }
	}
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err, string(out))
	}
	if len(source.NormalizedEvaluations) != len(rows) {
		t.Fatal("incomplete actual Source outer permission rows")
	}
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for i, row := range rows {
			context := protocol.NewJSONObject(field("alwaysAllowRules", map[string][]string{}), field("alwaysDenyRules", map[string][]string{}), field("alwaysAskRules", map[string][]string{}))
			for key, value := range row["context"].(map[string]any) {
				context.Set(key, raw(value))
			}
			*permission = context
			var input protocol.JSONObject
			_ = input.UnmarshalJSON(raw(row["input"]))
			decision, err := s.PermissionDecision(scope, "PowerShell", &input)
			if err != nil {
				return err
			}
			var got, want any
			_ = json.Unmarshal(raw(powerShellDecisionProjection(decision)), &got)
			_ = json.Unmarshal(source.NormalizedEvaluations[i].Result, &want)
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("Source normalization row%d %+v native=%s Source=%s", i, row, raw(powerShellDecisionProjection(decision)), source.NormalizedEvaluations[i].Result)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
