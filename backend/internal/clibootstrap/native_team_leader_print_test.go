package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActualHeadlessCLILeaderWaitsForPhysicalWorkerMailboxShutdownAndReturnsAfterCleanup(t *testing.T) {
	var mu sync.Mutex
	parentPosts, childPosts := 0, 0
	target := ""
	requestID := ""
	requestedShutdown := false
	childModels := []string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(string(body), "# Agent Teammate Communication") {
			childPosts++
			childModels = append(childModels, objMain(body).String("model"))
			switch childPosts {
			case 1:
				emitNativeTaskTool(w, "print-worker-write", "Write", map[string]any{"file_path": target, "content": "PHYSICAL_HEADLESS_TEAM_ARTIFACT"})
			case 2:
				emitNativeTaskTool(w, "print-worker-send", "SendMessage", map[string]any{"to": "team-lead", "message": "PHYSICAL_HEADLESS_TEAM_ARTIFACT ready", "summary": "real artifact"})
			case 3:
				emitPlanFixture(w, 3, "", "actual worker idle")
			case 4:
				var request struct{ Messages []protocol.JSONObject }
				_ = json.Unmarshal(body, &request)
				for _, message := range request.Messages {
					var text string
					if json.Unmarshal(message.Get("content"), &text) != nil {
						for _, block := range taskBlocks(message.Get("content")) {
							if block.String("type") == "text" {
								text += block.String("text")
							}
						}
					}
					start := strings.Index(text, `{"type":"shutdown_request"`)
					if start < 0 {
						continue
					}
					end := strings.Index(text[start:], "\n</teammate-message>")
					if end < 0 {
						continue
					}
					requestID = objMain([]byte(text[start : start+end])).String("requestId")
				}
				if requestID == "" {
					t.Error("actual worker never received shutdown request", string(body))
					http.Error(w, "missing shutdown", 500)
					return
				}
				emitNativeTaskTool(w, "print-worker-approve", "SendMessage", map[string]any{"to": "team-lead", "message": map[string]any{"type": "shutdown_response", "request_id": requestID, "approve": true}})
			default:
				t.Error("extra physical worker POST", childPosts)
				http.Error(w, "unexpected", 500)
			}
			return
		}
		parentPosts++
		switch parentPosts {
		case 1:
			emitNativeTaskTool(w, "print-team-create", "TeamCreate", map[string]any{"team_name": "actual-print-team"})
		case 2:
			emitNativeTaskTool(w, "print-team-spawn", "Agent", map[string]any{"name": "worker", "prompt": "write the real artifact and send it to the leader", "description": "physical worker"})
		case 3:
			emitPlanFixture(w, 3, "", "Waiting for actual worker result.")
		default:
			var request struct{ Messages []protocol.JSONObject }
			_ = json.Unmarshal(body, &request)
			if len(request.Messages) > 0 {
				last := request.Messages[len(request.Messages)-1]
				for _, block := range taskBlocks(last.Get("content")) {
					if block.String("type") == "tool_result" && block.String("tool_use_id") == "print-shutdown-request" {
						emitPlanFixture(w, parentPosts, "", "Waiting for actual shutdown approval.")
						return
					}
				}
			}
			if strings.Contains(string(body), `"tool_use_id":"print-team-delete"`) {
				emitPlanFixture(w, parentPosts, "", "FINAL_HEADLESS_TEAM_COMPLETE")
				return
			}
			if strings.Contains(string(body), "shutdown_approved") {
				if strings.Contains(string(body), `"name":"TeamDelete"`) {
					emitNativeTaskTool(w, "print-team-delete", "TeamDelete", map[string]any{})
					return
				}
				emitPlanFixture(w, parentPosts, "", "FINAL_HEADLESS_TEAM_COMPLETE")
				return
			}
			if requestedShutdown {
				emitPlanFixture(w, parentPosts, "", "Waiting for actual shutdown approval.")
				return
			}
			if parentPosts <= 7 {
				requestedShutdown = true
				emitNativeTaskTool(w, "print-shutdown-request", "SendMessage", map[string]any{"to": "worker", "message": map[string]any{"type": "shutdown_request", "reason": "real task complete"}})
				return
			}
			t.Error("leader repeated shutdown without consuming approval", string(body))
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":true}`), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,Write,Read,SendMessage"}
		o.Arguments.Replay = true
	})
	target = filepath.Join(m.workingDirectory(), "headless-worker.txt")
	m.options.Input = strings.NewReader(`{"type":"user","uuid":"20000000-0000-4000-8000-000000000001","message":{"role":"user","content":"Complete a real team task, wait for the worker and shut it down."}}` + "\n")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	code, err := m.Run(ctx)
	if err != nil || code != 0 {
		t.Fatal("actual CLI headless flow failed", code, err, parentPosts, childPosts)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "PHYSICAL_HEADLESS_TEAM_ARTIFACT" {
		t.Fatal("physical artifact missing", err, string(data))
	}
	if _, err = os.Stat(m.teamLeader.config.Path("actual-print-team")); !os.IsNotExist(err) {
		t.Fatal("CLI returned before physical team cleanup", err)
	}
	mu.Lock()
	children := childPosts
	nativeChildModels := append([]string{}, childModels...)
	mu.Unlock()
	if children != 4 {
		t.Fatal("worker shutdown reply caused extra POST", children)
	}
	if bun := os.Getenv("CC_HAHA_SOURCE_BUN"); bun != "" {
		home, _ := filepath.EvalSymlinks(t.TempDir())
		sourceTarget := filepath.Join(home, "headless-worker.txt")
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":true}`), 0600); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		target = sourceTarget
		parentPosts = 0
		childPosts = 0
		requestID = ""
		requestedShutdown = false
		childModels = []string{}
		mu.Unlock()
		fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-headless-complete.fixture.ts")
		sourceCtx, stop := context.WithTimeout(context.Background(), 35*time.Second)
		defer stop()
		cmd := exec.CommandContext(sourceCtx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CLAUDE_CODE_TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_COMPLETE_HEADLESS_TEAM_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_AGENT_TEAMS_ENABLED=1", "CLAUDE_CODE_ENABLE_TASKS=1", "CLAUDE_CODE_DISABLE_ATTACHMENTS=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=true", "ENABLE_TOOL_SEARCH=false", "ANTHROPIC_API_KEY=source-private-fake-key", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_MAX_RETRIES=0", "PATH=/usr/bin:/bin"}
		cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "origin": peer.URL, "target": sourceTarget}))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			t.Fatal("actual original runHeadless failed", err, stderr.String(), string(output))
		}
		receipt, err := os.ReadFile(filepath.Join(home, "headless-team-result.json"))
		if err != nil {
			t.Fatal(err, stderr.String(), string(output))
		}
		var source struct {
			Code         int
			Artifact     string
			TeamExists   bool
			Capabilities struct{ Blocked []string }
		}
		if err = json.Unmarshal(receipt, &source); err != nil || source.Code != 0 || source.Artifact != "PHYSICAL_HEADLESS_TEAM_ARTIFACT" || source.TeamExists || len(source.Capabilities.Blocked) > 0 {
			t.Fatal("actual Source receipt", err, string(receipt), stderr.String(), string(output))
		}
		mu.Lock()
		sourceChildren := childPosts
		sourceModels := append([]string{}, childModels...)
		mu.Unlock()
		if sourceChildren != children {
			t.Fatal("actual Source child lifecycle differs", sourceChildren, children, string(receipt))
		}
		if !reflect.DeepEqual(nativeChildModels, sourceModels) {
			t.Fatal("actual Source per-turn model choices differ", nativeChildModels, sourceModels)
		}
	}
}
