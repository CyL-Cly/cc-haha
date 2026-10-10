package clibootstrap

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

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualSourceTeamsOptInConsumersAndExternalCommanderRejectInternalFlag(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{
		{"env": map[string]string{}},
		{"env": map[string]string{"CC_HAHA_AGENT_TEAMS_ENABLED": "1"}},
		{"env": map[string]string{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1"}},
		{"env": map[string]string{"CC_HAHA_AGENT_TEAMS_DEFAULT": "true"}},
		{"env": map[string]string{"CC_HAHA_AGENT_TEAMS_ENABLED": "0", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1", "USER_TYPE": "ant"}},
		{"env": map[string]string{"USER_TYPE": "ant"}},
		{"env": map[string]string{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "0", "CC_HAHA_AGENT_TEAMS_DEFAULT": "1"}},
		{"env": map[string]string{"CC_HAHA_AGENT_TEAMS_ENABLED": " YES "}},
		{"env": map[string]string{"CC_HAHA_AGENT_TEAMS_ENABLED": ""}},
		// The original library sees raw argv; the actual external CLI rejects
		// this unregistered ant-only option before reaching those consumers.
		{"env": map[string]string{}, "args": []string{"--agent-teams"}},
		{"env": map[string]string{"CC_HAHA_AGENT_TEAMS_ENABLED": "0"}, "args": []string{"--agent-teams"}},
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-enabled.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_ENABLED_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(out))
	}
	var source struct {
		CLI struct {
			ExitCode       int
			Stdout, Stderr string
			Parsed         any
			Blocked        []string
		}
		Rows         []map[string]bool
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out), stderr.String())
	}
	for _, blocked := range source.CLI.Blocked {
		if blocked != "subprocess:execFile:security" {
			t.Fatal("original CLI inventory escaped its private subprocess guard", blocked)
		}
	}
	parsed := cliargs.Parse([]string{"--agent-teams"}, cliargs.Config{Version: "999.0.0-local"})
	if parsed.ExitCode != source.CLI.ExitCode || parsed.Stdout != source.CLI.Stdout || parsed.Stderr != source.CLI.Stderr || source.CLI.Parsed != nil || parsed.Parsed != nil {
		t.Fatal("actual external parser differs", parsed, string(out))
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read,TeamCreate,TeamDelete,SendMessage"} })
	for i, row := range rows {
		if _, rawArgv := row["args"]; rawArgv {
			want := i == len(rows)-2
			if source.Rows[i]["enabled"] != want || source.Rows[i]["create"] != want || source.Rows[i]["send"] != want {
				t.Fatal("actual library argv probe changed", source.Rows[i])
			}
			continue
		}
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			for _, key := range []string{"USER_TYPE", "CC_HAHA_AGENT_TEAMS_ENABLED", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS", "CC_HAHA_AGENT_TEAMS_DEFAULT"} {
				delete(m.options.Native.Environment, key)
			}
			for key, value := range row["env"].(map[string]string) {
				m.options.Native.Environment[key] = value
			}
			enabled, err := m.nativeSendMessageEnabled(scope)
			if err != nil {
				return err
			}
			defs, _, err := m.nativeTools.Catalog(scope)
			if err != nil {
				return err
			}
			got := map[string]bool{"enabled": enabled, "create": false, "send": false}
			for _, def := range defs {
				if def.Name == "TeamCreate" {
					got["create"] = true
				}
				if def.Name == "SendMessage" {
					got["send"] = true
				}
			}
			if !reflect.DeepEqual(got, source.Rows[i]) {
				t.Errorf("case%d Go%v Source%v", i, got, source.Rows[i])
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
