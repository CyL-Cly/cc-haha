package application

import (
	"strings"
	"testing"
)

func TestPaneCLIIdentityOptionTupleOptOutAndInheritedRuntimeConsumers(t *testing.T) {
	home := t.TempDir()
	args := []string{"--agent-id", "worker@team", "--agent-name", "worker", "--team-name", "team", "--agent-color", "blue", "--parent-session-id", "parent", "--agent-type", "general-purpose", "--plan-mode-required", "--teammate-mode", "tmux", "--effort", "high", "--thinking", "disabled", "--plugin-dir", home, "--no-chrome"}
	enabled := map[string]string{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1"}
	command, _, err := ParseCommand(args, "fixture", home, home, enabled)
	if err != nil {
		t.Fatal(err)
	}
	if command.PaneTeammate == nil || command.PaneTeammate.AgentID != "worker@team" || !command.PaneTeammate.PlanModeRequired || command.Arguments.TeammateMode != "tmux" || command.Arguments.EffortLevel != "high" || command.Arguments.Thinking.Type != "disabled" || len(command.Arguments.PluginDirectories) != 1 || command.Arguments.PluginDirectories[0] != home || command.Arguments.Chrome == nil || *command.Arguments.Chrome {
		t.Fatal("inherited Source CLI producer lost consumer", command)
	}
	for _, args := range [][]string{{"--agent-id", "worker@team"}, {"--agent-name", "worker", "--team-name", "team"}} {
		_, _, err := ParseCommand(args, "fixture", home, home, enabled)
		if err == nil || !strings.Contains(err.Error(), "must all be provided together") {
			t.Fatal("partial identity", err)
		}
	}
	command, _, err = ParseCommand(args, "fixture", home, home, map[string]string{"CC_HAHA_AGENT_TEAMS_ENABLED": "0", "USER_TYPE": "ant"})
	if err != nil || command.PaneTeammate != nil {
		t.Fatal("Source authoritative optout changed", command, err)
	}
	command, _, err = ParseCommand([]string{"--chrome"}, "fixture", home, home, enabled)
	if err != nil || command.Arguments.Chrome == nil || !*command.Arguments.Chrome {
		t.Fatal("Chrome flag lost its integration startup consumer", command, err)
	}
	command, _, err = ParseCommand([]string{"--chrome", "--no-chrome"}, "fixture", home, home, enabled)
	if err != nil || command.Arguments.Chrome == nil || *command.Arguments.Chrome {
		t.Fatal("Source final Chrome opt-out was blocked by an earlier option", command, err)
	}
	command, _, err = ParseCommand([]string{"--no-chrome", "--chrome"}, "fixture", home, home, enabled)
	if err != nil || command.Arguments.Chrome == nil || !*command.Arguments.Chrome {
		t.Fatal("final active Chrome flag lost its integration consumer", command, err)
	}
}
