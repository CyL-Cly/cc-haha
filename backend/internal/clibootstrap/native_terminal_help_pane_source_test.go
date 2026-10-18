package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

func TestTerminalHelpActualSourceGenericOnDonePaneMAINCanonicalAndNextHarness(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original Source required")
	}
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "# Agent Teammate Communication") {
			t.Error("actual pane MAIN role absent", string(body))
		}
		if !strings.Contains(string(body), "<command-name>/help</command-name>") || !strings.Contains(string(body), "<local-command-stdout>Help dialog dismissed</local-command-stdout>") {
			t.Error("actual Source local command model projection absent", string(body))
		}
		emitPlanFixture(w, 1, "", "HELP_PANE_NEXT_DONE")
	}))
	defer peer.Close()
	identity := teamspawn.PaneIdentity{AgentID: "worker@owned-help-pane", AgentName: "worker", TeamName: "owned-help-pane", ParentSessionID: "parent", Color: "blue"}
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1" }, func(o *MainPrintOptions) { o.Interactive = true; o.PaneTeammate = &identity })
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-help-v2.fixture.ts")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HELP_V2_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "COLUMNS=100", "LINES=80", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "rows": []any{map[string]any{"caller": true, "args": "owned pane help args", "width": 100, "height": 80, "commands": []any{}, "keys": []string{"\x1b"}, "pane": map[string]any{"agentId": identity.AgentID, "agentName": identity.AgentName, "teamName": identity.TeamName, "parentSessionId": identity.ParentSessionID, "planModeRequired": false}}}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("original help pane caller", err, string(output))
	}
	var rows []struct {
		SettledBeforeKeys, PromiseSettled bool
		PaneAgentId                       string
		NormalizedAPI                     []protocol.JSONObject
		Completion                        struct {
			Messages    []protocol.JSONObject
			ShouldQuery bool
		}
	}
	if err = json.Unmarshal(output, &rows); err != nil || len(rows) != 1 || rows[0].SettledBeforeKeys || !rows[0].PromiseSettled || rows[0].PaneAgentId != identity.AgentID || rows[0].Completion.ShouldQuery || len(rows[0].Completion.Messages) != 2 {
		t.Fatal("actual original caller lifetime", err, string(output))
	}
	if len(rows[0].NormalizedAPI) != 1 || rows[0].NormalizedAPI[0].String("type") != "user" || !strings.Contains(string(rows[0].NormalizedAPI[0].Get("message")), "<command-name>/help</command-name>") || !strings.Contains(string(rows[0].NormalizedAPI[0].Get("message")), "Help dialog dismissed") {
		t.Fatal("actual original normalizeMessagesForAPI local systems", string(output))
	}
	if _, err = m.CompleteTerminalLocalJSXRecords(context.Background(), "mobile", "", nil, "skip", false); err == nil {
		t.Fatal("pane Help admission allowed unrelated local command")
	}
	actual, err := m.CompleteTerminalLocalJSXRecords(context.Background(), "help", "owned pane help args", quoteMain("Help dialog dismissed"), "system", false)
	if err != nil {
		t.Fatal("actual pane MAIN help completion", err)
	}
	semantic := func(values []protocol.JSONObject) []string {
		out := []string{}
		for _, v := range values {
			out = append(out, v.String("type")+":"+v.String("subtype")+":"+v.String("content"))
		}
		return out
	}
	if !reflect.DeepEqual(semantic(actual), semantic(rows[0].Completion.Messages)) {
		t.Fatal("pane canonical messages differ", semantic(actual), semantic(rows[0].Completion.Messages))
	}
	input := protocol.NewJSONObject(fieldMain("type", "user"), protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Actual next pane MAIN human"})})
	if err = m.RunTurn(context.Background(), input); err != nil || requests.Load() != 1 {
		t.Fatal("actual same Harness after Help", err, requests.Load())
	}
}
