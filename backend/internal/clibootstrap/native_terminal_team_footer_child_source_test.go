package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

func TestTerminalPaneChildActualSourceInitialRoleDoesNotProjectFileMembersAsFooterRoster(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	identity := teamspawn.PaneIdentity{AgentID: "worker@footer-child", AgentName: "worker", TeamName: "footer-child", ParentSessionID: "parent-source-owned", Color: "blue"}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["TMUX"] = "owned-child-launch"
	}, func(options *MainPrintOptions) { options.Interactive = true; options.PaneTeammate = &identity })
	initial, unsubscribe, err := m.SubscribeTerminalTeamFooter(context.Background(), nil)
	if err != nil || initial {
		t.Fatal("child published a parent/File roster", initial, err)
	}
	defer unsubscribe()
	if m.teamWorker == nil || m.teamWorker.pane == nil || m.teamState != nil {
		t.Fatal("actual native pane role was not used")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-team-footer.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAM_FOOTER_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "modes": []any{}, "footers": []any{}, "children": []any{map[string]any{"name": "actual-role", "role": map[string]any{"teamName": identity.TeamName, "agentId": identity.AgentID, "agentName": identity.AgentName}, "file": map[string]any{"leadAgentId": "team-lead@footer-child", "members": []any{map[string]string{"name": "team-lead"}, map[string]string{"name": "worker"}, map[string]string{"name": "other-real-file-member"}}}}}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var original struct {
		Children []struct {
			Context  protocol.JSONObject
			HasTeams bool
		}
	}
	if err = json.Unmarshal(output, &original); err != nil {
		t.Fatal(err, string(output))
	}
	if len(original.Children) != 1 || original.Children[0].HasTeams || original.Children[0].Context.String("selfAgentId") != identity.AgentID || string(original.Children[0].Context.Get("teammates")) != "{}" {
		t.Fatal("actual Source child reconstruction changed", string(output))
	}
}
