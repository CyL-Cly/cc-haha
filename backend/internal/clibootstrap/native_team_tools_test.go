package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
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
)

func emitTeamSubmitBatch(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(value string) {
		var v protocol.JSONObject
		_ = v.UnmarshalJSON([]byte(value))
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", v.String("type"), value)
	}
	send(`{"type":"message_start","message":{"id":"team-model","type":"message","role":"assistant","model":"fixture-model","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`)
	for i, tool := range []struct{ name, id, input string }{{"TeamPlan", "submit-plan", `{"team_name":"turn-boundary","operation":"submit","expected_revision":1}`}, {"TaskCreate", "after-submit", `{"subject":"Batch after submit","description":"All results before pause"}`}} {
		send(string(rawMain(map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": tool.id, "name": tool.name, "input": map[string]any{}}})))
		send(string(rawMain(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": tool.input}})))
		send(string(rawMain(map[string]any{"type": "content_block_stop", "index": i})))
	}
	send(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`)
	send(`{"type":"message_stop"}`)
}
func callActualTeamTool(t *testing.T, m *MainPrint, name, input string) protocol.JSONObject {
	t.Helper()
	var data protocol.JSONObject
	err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var in protocol.JSONObject
		if err := in.UnmarshalJSON([]byte(input)); err != nil {
			return err
		}
		_, tools, err := m.nativeTools.Catalog(scope)
		if err != nil {
			return err
		}
		for _, tool := range tools {
			if tool.Name == name {
				parsed, err := tool.ParseInput(in)
				if err != nil {
					return err
				}
				if tool.ValidateInput != nil {
					if err := tool.ValidateInput(context.Background(), parsed, &builtinwrite.CallContext{Scope: scope}); err != nil {
						return err
					}
				}
				future, err := tool.StartCallJSON(context.Background(), parsed, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
				if err != nil {
					return err
				}
				result, err := future.Await(scope)
				if err != nil {
					return err
				}
				return data.UnmarshalJSON(result.Data)
			}
		}
		return fmt.Errorf("actual tool not enabled: %s", name)
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestActualTeamLeaderSharedHarnessSubmitBatchPauseAndNextHumanTurnMatchesSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"is_error":true`)) {
			t.Errorf("tool error %s", body)
		}
		round := posts.Add(1)
		if round == 1 {
			emitTeamSubmitBatch(w)
		} else {
			emitWorktreeModel(w, int(round), "", "", "Continue only after human input.")
		}
	}))
	defer peer.Close()
	home := t.TempDir()
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	home = canonical
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false}}`), 0600)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-turn-boundary.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_TURN_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=fake-private-key", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_CODE_ENABLE_TASKS=1", "CLAUDE_CODE_DISABLE_ATTACHMENTS=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=true", "ENABLE_TOOL_SEARCH=false", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "origin": peer.URL}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(out))
	}
	var source struct {
		Observations []struct {
			Terminal    protocol.JSONObject
			Output      []protocol.JSONObject
			Attachments int
			Aborted     bool
		}
		Plan         protocol.JSONObject
		Tasks        []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Observations) != 2 || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	if posts.Load() != 2 || source.Observations[0].Terminal.String("reason") != "completed" || source.Observations[0].Attachments != 0 || source.Observations[0].Aborted || source.Plan.String("state") != "review_pending" || len(source.Tasks) != 1 {
		t.Fatal("Source boundary", string(out), posts.Load())
	}
	posts.Store(0)
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CC_HAHA_TEAM_REVIEW_REQUIRED"] = "1"
		n.Environment["CC_HAHA_TEAM_LEADER_RUNTIME"] = `{"providerId":"fixture-provider","modelId":"fixture-model"}`
		n.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
		n.Environment["ENABLE_TOOL_SEARCH"] = "false"
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"TeamCreate,TeamPlan,TeamDelete,TaskCreate,TaskGet,TaskList,TaskUpdate"}
	})
	created := callActualTeamTool(t, m, "TeamCreate", `{"team_name":"turn-boundary","plan":{"members":[{"id":"m","name":"worker","prompt":"Wait for actual human review."}],"tasks":[{"id":"task","subject":"Reviewed task","ownerId":"m"}]}}`)
	if created.String("team_name") != "turn-boundary" || created.Get("plan") == nil {
		t.Fatal(string(rawMain(created)))
	}
	input := func(content string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": content})})
	}
	if err = m.RunTurn(ctx, input("Submit the proposal.")); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatalf("submit performed another model request: %d", posts.Load())
	}
	plan, err := m.teamLeader.plans.Read("turn-boundary")
	if err != nil || plan == nil || plan.String("state") != source.Plan.String("state") || string(plan.Get("revision")) != string(source.Plan.Get("revision")) {
		t.Fatal(err, plan)
	}
	ids := []string{}
	history := m.Controller.History()
	for _, message := range history {
		var envelope protocol.JSONObject
		_ = envelope.UnmarshalJSON(message.Get("message"))
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(envelope.Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") == "tool_result" {
				ids = append(ids, block.String("tool_use_id"))
				if string(block.Get("is_error")) == "true" {
					t.Errorf("Go error %s", rawMain(block))
				}
			}
		}
	}
	if strings.Join(ids, ",") != "submit-plan,after-submit" {
		t.Fatal("full batch results missing", ids)
	}
	if err = m.RunTurn(ctx, input("Continue after review.")); err != nil || posts.Load() != 2 {
		t.Fatal("next human", err, posts.Load())
	}
	tasks, err := m.taskStore.List("turn-boundary")
	if err != nil || len(tasks) != 1 || tasks[0].String("subject") != source.Tasks[0].String("subject") {
		t.Fatal("shared task identity", err, tasks)
	}
	deleted := callActualTeamTool(t, m, "TeamDelete", `{}`)
	if string(deleted.Get("success")) != "true" || m.nativeLeaderTeamName() != "" {
		t.Fatal("actual delete", string(rawMain(deleted)))
	}
	if _, err = os.Stat(m.teamLeader.config.Directory("turn-boundary")); !os.IsNotExist(err) {
		t.Fatal("team directory retained", err)
	}
}
