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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalFullscreenHelpActualGenericOnDoneSkipsOnlyDismissalAndNextHarness(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original fullscreen Help required")
	}
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "<command-name>/help</command-name>") || strings.Contains(string(body), "Help dialog dismissed") || !strings.Contains(string(body), "Actual next fullscreen human") {
			t.Error("Fullscreen dismissal changed actual next model history", string(body))
		}
		emitPlanFixture(w, 1, "", "FULLSCREEN_HELP_NEXT")
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { n.Environment["CLAUDE_CODE_NO_FLICKER"] = "1" }, func(o *MainPrintOptions) { o.Interactive = true })
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-help-fullscreen.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FULLSCREEN_HELP_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "COLUMNS=100", "LINES=36", "PATH=/usr/bin:/bin", "USER_TYPE=external"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "rows": []any{map[string]any{"width": 100, "height": 36, "args": "actual fullscreen retained args", "commands": []any{}, "keys": []string{"\x1b"}}}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("Actual original fullscreen caller", err, string(output))
	}
	var source []struct {
		FullscreenEnabled bool
		Completion        struct {
			Messages    []protocol.JSONObject
			ShouldQuery bool
		}
		Snapshots []struct{ Settled, AltScreen bool }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source) != 1 || !source[0].FullscreenEnabled || len(source[0].Completion.Messages) != 0 || source[0].Completion.ShouldQuery || len(source[0].Snapshots) != 2 || source[0].Snapshots[0].Settled || !source[0].Snapshots[0].AltScreen || !source[0].Snapshots[1].Settled {
		t.Fatal("Original fullscreen completion boundary", err, string(output))
	}
	actual, err := m.CompleteTerminalLocalJSXRecords(context.Background(), "help", "actual fullscreen retained args", quoteMain("Help dialog dismissed"), "system", false)
	if err != nil || len(actual) != len(source[0].Completion.Messages) {
		t.Fatal("Go fullscreen Help differs from original actual onDone", err, actual, source[0].Completion)
	}
	input := protocol.NewJSONObject(fieldMain("type", "user"), protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Actual next fullscreen human"})})
	if err = m.RunTurn(context.Background(), input); err != nil || requests.Load() != 1 {
		t.Fatal("Same Harness after fullscreen Help", err, requests.Load())
	}
}
