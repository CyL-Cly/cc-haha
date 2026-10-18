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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

func TestActualPaneMainBuddyStartupProjectionAgainstWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-buddy.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, seed := range []string{
		`{"userID":"owned-pane-buddy","futureBuddy":{"keep":41}}`,
		`{"userID":"owned-pane-buddy","companion":{"name":"Pane Buddy","personality":"curious","appearanceSeed":"owned-pane-seed","hatchedAt":17,"futureSoul":{"keep":true}},"futureBuddy":{"keep":41}}`,
		`{"userID":"owned-pane-buddy","companion":{"name":"\ud800","personality":"Old soul\ud801","hatchedAt":0},"companionMuted":true,"futureBuddy":{"keep":41}}`,
	} {
		t.Run(seed, func(t *testing.T) {
			var requests atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				requests.Add(1)
				if !strings.Contains(string(body), "# Agent Teammate Communication") {
					t.Error("pane lost original MAIN identity", string(body))
				}
				emitPlanFixture(w, 1, "", "PANE_BUDDY_NEXT_MAIN_DONE")
			}))
			defer peer.Close()
			identity := teamspawn.PaneIdentity{AgentID: "worker@owned-buddy-pane", AgentName: "worker", TeamName: "owned-buddy-pane", ParentSessionID: "parent", Color: "blue"}
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1" }, func(o *MainPrintOptions) {
				o.Interactive = true
				o.PaneTeammate = &identity
			})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			global := objMain([]byte(seed))
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = m.options.Native.Home
			cmd.Env = []string{"HOME=" + m.options.Native.Home, "USERPROFILE=" + m.options.Native.Home, "CLAUDE_CONFIG_DIR=" + filepath.Join(m.options.Native.Home, ".claude"), "TMPDIR=" + m.options.Native.Home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(m.options.Native.Home), "CC_HAHA_BUDDY_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": m.options.Native.Home, "rows": []any{map[string]any{"global": global}}}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("whole original companion producer %v %.6500s", err, output)
			}
			var source []struct {
				Stored json.RawMessage
				Roll   struct{ Bones json.RawMessage }
			}
			if err := json.Unmarshal(output, &source); err != nil || len(source) != 1 {
				t.Fatal("whole Source companion shape", err, string(output))
			}
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					next := current.Clone()
					for _, name := range []string{"companion", "companionMuted", "userID", "futureBuddy"} {
						if value := global.Get(name); value != nil {
							next.Set(name, value)
						} else {
							next.Delete(name)
						}
					}
					return &next, nil
				})
			}); err != nil {
				t.Fatal(err)
			}
			projection, err := m.TerminalBuddySnapshot(ctx)
			if err != nil {
				t.Fatal("actual pane MAIN cannot mount its original companion consumer", err)
			}
			value := objMain(projection)
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(value.Get("companion")), protocol.StringifyValidatedJavaScriptJSON(source[0].Stored)) {
				t.Fatal("pane original companion projection", string(projection), string(source[0].Stored))
			}
			if string(source[0].Stored) == "null" && !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(value.Get("preview")), protocol.StringifyValidatedJavaScriptJSON(source[0].Roll.Bones)) {
				t.Fatal("pane original empty companion preview", string(projection), string(source[0].Roll.Bones))
			}
			if requests.Load() != 0 || m.agentChild || m.teamWorker == nil || m.teamWorker.pane == nil {
				t.Fatal("startup view changed original MAIN role or ran model", requests.Load())
			}
			input := protocol.NewJSONObject(fieldMain("type", "user"), protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Actual next pane MAIN human"})})
			if err := m.RunTurn(ctx, input); err != nil {
				t.Fatal("same pane MAIN after view", err)
			}
			if requests.Load() != 1 {
				t.Fatal("next pane MAIN request count", requests.Load())
			}
		})
	}
}
