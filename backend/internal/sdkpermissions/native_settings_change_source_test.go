package sdkpermissions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSettingsApplyRulesAndBypassAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	rows := []map[string]any{}
	got := []json.RawMessage{}
	for _, managed := range []bool{false, true} {
		for _, mode := range []string{"default", "acceptEdits", "bypassPermissions"} {
			for _, disable := range []bool{false, true} {
				owner, _, gate := setup(t, "")
				policy := protocol.NewJSONObject()
				policy.Set("allowManagedPermissionRulesOnly", []byte("false"))
				if managed {
					policy.Set("allowManagedPermissionRulesOnly", []byte("true"))
				}
				global := protocol.NewJSONObject()
				producer, err := NewNativeStartup(owner, NativeStartupOptions{Home: home, CWD: home, OriginalCWD: home, Platform: "darwin", Environment: func(string) string { return "" }, Global: func() (*protocol.JSONObject, error) { return &global, nil }, Policy: func() (*protocol.JSONObject, error) { return &policy, nil }, Stat: os.Stat, Lstat: os.Lstat, Realpath: filepath.EvalSymlinks})
				if err != nil {
					t.Fatal(err)
				}
				settings := protocol.NewJSONObject()
				next := protocol.NewJSONObject()
				if disable {
					next.Set("permissions", []byte(`{"disableBypassPermissionsMode":"disable"}`))
				}
				rules := map[string]RuleSet{"userSettings": {Allow: []string{"Task", `Bash(*)`, `Read(file\(name\))`}, Deny: []string{"Write"}}, "localSettings": {Ask: []string{"Read"}}, "flagSettings": {Allow: []string{"KillShell"}}, "policySettings": {Allow: []string{"AgentOutputTool"}, Deny: []string{"Read(../secret)"}}}
				r := rules["userSettings"]
				r.Deny = append(r.Deny, "Skill(same-\ufffd:reader)")
				r.RawSkillDeny = []json.RawMessage{nil, json.RawMessage(`"Skill(same-\ud800:reader)"`)}
				r.Allow = append(r.Allow, "Skill(same-\ufffd:*)")
				r.RawSkillAllow = []json.RawMessage{nil, nil, nil, json.RawMessage(`"Skill(same-\udc00:*)"`)}
				rules["userSettings"] = r

				err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					if _, err := producer.Prepare(scope, &settings, NativeStartupRequest{Mode: mode, AllowDangerouslySkipPermissions: true}); err != nil {
						return err
					}
					state, err := Initialize(scope, owner, &settings, gate, Input{Profile: PublicPrintProfile, NativeProcess: true, NativeStartup: producer, Platform: "darwin", Model: "actual", NonInteractive: true, BaseToolsCLI: []string{"Read", "Write"}, Allowed: []string{"Read"}, Mode: mode, AllowBypass: true})
					if err != nil {
						return err
					}
					if err = state.ApplyPermissionUpdates(scope, ParsePermissionUpdates([]byte(`[{"type":"addRules","rules":[{"toolName":"Write"}],"behavior":"allow","destination":"session"}]`)), nil, nil); err != nil {
						return err
					}
					_, before, err := state.Read(scope)
					if err != nil {
						return err
					}
					rulesInput := map[string]any{}
					for source, r := range rules {
						rulesInput[source] = map[string]any{"allow": r.nativeRulesJSON("alwaysAllowRules", false), "deny": r.nativeRulesJSON("alwaysDenyRules", false), "ask": r.nativeRulesJSON("alwaysAskRules", false)}
					}
					rows = append(rows, map[string]any{"context": before, "rules": rulesInput, "settings": next, "policy": policy})
					if err = state.ApplyNativeSettingsChange(scope, &next, rules); err != nil {
						return err
					}
					actual, permission, err := state.Read(scope)
					if err != nil {
						return err
					}
					if actual != &next {
						return cap("actual settings reference replaced")
					}
					raw, _ := permission.MarshalJSON()
					got = append(got, protocol.StringifyValidatedJavaScriptJSON(raw))
					return nil
				})
				owner.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-settings-apply.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SETTINGS_APPLY_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
	payload, _ := json.Marshal(map[string]any{"rows": rows})
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("actual settings apply %v %s", err, stderr.String())
	}
	var source []json.RawMessage
	if err = json.Unmarshal(output, &source); err != nil || len(source) != len(got) {
		t.Fatalf("Source outputs %v %s", err, output)
	}
	for i, expected := range source {
		if !bytes.Equal(got[i], protocol.StringifyValidatedJavaScriptJSON(expected)) {
			t.Errorf("row%d context%s want%s", i, got[i], expected)
		}
	}
}
