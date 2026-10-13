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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeAutoFreshGateAndStartupStrippingAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type inputRow struct {
		Mode      string         `json:"mode"`
		User      string         `json:"userType"`
		Settings  map[string]any `json:"settings"`
		Overrides map[string]any `json:"overrides"`
		CLI, Fast bool
	}
	rows := []inputRow{}
	for _, mode := range []string{"auto", "plan", "default"} {
		for _, enabled := range []string{"opt-in", "enabled", "disabled"} {
			for _, opted := range []bool{false, true} {
				for _, fast := range []bool{false, true} {
					rows = append(rows, inputRow{Mode: mode, User: "ant", Settings: map[string]any{"skipAutoPermissionPrompt": opted, "permissions": map[string]any{"allow": []string{"Bash(*)", "Agent", "Read"}}}, Overrides: map[string]any{"tengu_auto_mode_config": map[string]any{"enabled": enabled, "disableFastMode": true}}, CLI: !opted, Fast: fast})
				}
			}
		}
	}
	for _, mode := range []string{"auto", "plan", "default"} {
		rows = append(rows, inputRow{Mode: mode, User: "external", Settings: map[string]any{"permissions": map[string]any{"disableAutoMode": "disable", "allow": []string{"Bash(*)"}}}, CLI: true})
		rows = append(rows, inputRow{Mode: mode, User: "external", Settings: map[string]any{"permissions": map[string]any{"allow": []string{"Read"}}}})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/native-auto-gate.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AUTO_GATE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	// Retain the producer's public names, never change Source business logic.
	inputs := []map[string]any{}
	for _, row := range rows {
		inputs = append(inputs, map[string]any{"mode": row.Mode, "userType": row.User, "settings": row.Settings, "overrides": row.Overrides, "cliFlag": row.CLI, "fast": row.Fast})
	}
	payload, _ := json.Marshal(map[string]any{"home": home, "rows": inputs})
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source %v %.4000s", err, out)
	}
	var source struct {
		Rows []struct {
			Before, Stripped, Verified protocol.JSONObject
			Active                     bool
			Notification, Next         string
		}
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatalf("Source shape %v %.3000s", err, out)
	}
	decode := func(raw []byte) any {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	for i, row := range rows {
		owner := cooperative.New()
		err := owner.Run(ctx, func(scope *cooperative.Scope) error {
			merged := protocol.NewJSONObject()
			_ = merged.UnmarshalJSON(autoRaw(row.Settings))
			envOverrides, _ := json.Marshal(row.Overrides)
			startup := &NativeStartup{owner: owner, options: NativeStartupOptions{Environment: func(key string) string {
				if key == "USER_TYPE" {
					return row.User
				}
				if key == "CLAUDE_INTERNAL_FC_OVERRIDES" {
					return string(envOverrides)
				}
				return ""
			}, Global: func() (*protocol.JSONObject, error) { global := protocol.NewJSONObject(); return &global, nil }, SettingsForSource: func(name string) (*protocol.JSONObject, error) {
				if name == "userSettings" {
					return &merged, nil
				}
				return nil, nil
			}, InitialSettings: func() (*protocol.JSONObject, error) { return &merged, nil }}}
			state := &State{owner: owner, nativeProcess: true, nativeStartup: startup, context: source.Rows[i].Before.Clone(), autoModeActive: row.Mode != "default"}
			if row.Mode != "default" {
				if err := state.stripStartupAutoRules(); err != nil {
					return err
				}
			}
			if row.Mode == "plan" {
				if err := state.EnterPlanMode(scope); err != nil {
					return err
				}
			}
			if !reflect.DeepEqual(decode(autoRaw(state.context)), decode(autoRaw(source.Rows[i].Stripped))) {
				t.Errorf("case%d original startup/plan strip differs Go%s Source%s", i, autoRaw(state.context), autoRaw(source.Rows[i].Stripped))
			}
			notice, _, err := state.VerifyAutoModeGate(scope, row.CLI, row.Fast)
			if err != nil {
				return err
			}
			next, err := state.NextTerminalMode(scope, row.User)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(decode(autoRaw(state.context)), decode(autoRaw(source.Rows[i].Verified))) || state.autoModeActive != source.Rows[i].Active || notice != source.Rows[i].Notification || next != source.Rows[i].Next {
				t.Errorf("case%d gate differs Go%s active%v notice%q next%q Source%s active%v notice%q next%q", i, autoRaw(state.context), state.autoModeActive, notice, next, autoRaw(source.Rows[i].Verified), source.Rows[i].Active, source.Rows[i].Notification, source.Rows[i].Next)
			}
			return nil
		})
		owner.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
