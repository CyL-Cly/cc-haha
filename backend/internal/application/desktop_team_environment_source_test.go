package application

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
)

func TestDesktopTeamChildEnvironmentMatchesActualBuildChildEnv(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	if err = os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config, "CC_HAHA_AGENT_TEAMS_ENABLED": "stale", "CLAUDE_CODE_ENABLE_TASKS": "0", "CC_HAHA_TEAM_REVIEW_REQUIRED": "0", "CC_HAHA_TEAM_LEADER_RUNTIME": "broken"}
	rows := []map[string]any{}
	for _, enabled := range []bool{false, true} {
		for _, selection := range []map[string]any{{}, {"providerId": nil}, {"providerId": "saved"}, {"providerId": "saved", "model": "explicit-model", "effort": "high"}, {"providerId": "saved", "model": "  spaced-model  ", "effort": ""}, {"model": "", "effort": "low"}} {
			for _, model := range []string{"", "inherited-model"} {
				environment := map[string]string{}
				for key, value := range env {
					environment[key] = value
				}
				if model != "" {
					environment["ANTHROPIC_MODEL"] = model
				}
				row := map[string]any{"enabled": enabled, "selection": selection, "environment": environment}
				if selection["providerId"] == "saved" {
					row["providerEnvironment"] = map[string]string{"ANTHROPIC_MODEL": "provider-model"}
				}
				rows = append(rows, row)
			}
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-team-environment.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_DESKTOP_TEAM_ENV_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(data))
	}
	var source struct {
		Rows         []map[string]string
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(data), stderr.String())
	}
	for i, row := range rows {
		selection := row["selection"].(map[string]any)
		launch := &desktopTeamLaunch{}
		if provider, ok := selection["providerId"].(string); ok {
			launch.provider = &provider
		}
		launch.model, _ = selection["model"].(string)
		launch.effort, _ = selection["effort"].(string)
		environment := map[string]string{}
		for key, value := range row["environment"].(map[string]string) {
			environment[key] = value
		}
		// These are the real provider resolver's environment values at the
		// native boundary, after explicit provider isolation and model trim.
		if _, present := selection["providerId"]; present {
			delete(environment, "ANTHROPIC_MODEL")
		}
		if provider, ok := row["providerEnvironment"].(map[string]string); ok {
			environment["ANTHROPIC_MODEL"] = provider["ANTHROPIC_MODEL"]
		}
		got, err := desktopTeamEnvironment(ctx, Options{desktopTeamLaunch: launch, DesktopTeamsEnabled: func(context.Context) (bool, error) { return row["enabled"].(bool), nil }}, environment)
		if err != nil {
			t.Fatal(err)
		}
		actual := map[string]string{}
		for key := range source.Rows[i] {
			actual[key] = got[key]
		}
		if !reflect.DeepEqual(actual, source.Rows[i]) {
			t.Errorf("case%d Go%v Source%v", i, actual, source.Rows[i])
		}
	}
}
