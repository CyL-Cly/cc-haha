package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualInProcessPlanSpawnWritesSubmitsAutoApprovalAndNextPromptPreservesSourceTaskMode(t *testing.T) {
	awaiting, release, nextTurn := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var released atomic.Bool
	defer func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}()
	var requests, exitPrompts atomic.Int32
	planPath := ""
	plan := "ACTUAL_INPROCESS_LEADER_REVIEW_PLAN"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch requests.Add(1) {
		case 1:
			var body any
			_ = json.Unmarshal(raw, &body)
			var search func(any)
			search = func(value any) {
				switch v := value.(type) {
				case string:
					match := regexp.MustCompile(`create your plan at ([^\n]+?\.md)`).FindStringSubmatch(v)
					if len(match) > 1 {
						planPath = match[1]
					}
				case map[string]any:
					for _, part := range v {
						search(part)
					}
				case []any:
					for _, part := range v {
						search(part)
					}
				}
			}
			search(body)
			if planPath == "" {
				t.Error("real plan child did not receive original plan-file attachment")
				emitPlanFixture(w, 1, "", "missing-plan-path")
				return
			}
			emitNativeTaskTool(w, "actual-plan-write", "Write", map[string]string{"file_path": planPath, "content": plan})
		case 2:
			emitPlanFixture(w, 2, "ExitPlanMode", "")
		case 3:
			if !strings.Contains(string(raw), "Your plan has been submitted to the team lead for approval.") {
				t.Error("actual required ExitPlanMode consumer/mapping missing", string(raw))
			}
			close(awaiting)
			select {
			case <-r.Context().Done():
				return
			case <-release:
			}
			emitPlanFixture(w, 3, "", "waiting for original leader response")
		case 4:
			if !strings.Contains(string(raw), "plan_approval_response") || !strings.Contains(string(raw), `approved\":true`) {
				t.Error("next real sidechain did not receive original mailbox approval", string(raw))
			}
			emitPlanFixture(w, 4, "", "original approval received")
			close(nextTurn)
		default:
			t.Error("unexpected plan query", requests.Load())
			http.Error(w, "unexpected", 500)
		}
	}))
	defer provider.Close()
	parent := backgroundAgentMainWithOptions(t, provider.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		delete(n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.PermissionMode = "default"
		o.Arguments.Tools = []string{"Read,Write,ExitPlanMode,SendMessage,TeamCreate,TeamDelete"}
		o.ResolvePermission = func(_ context.Context, _ *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, _ queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
			if tool.Name == "ExitPlanMode" {
				exitPrompts.Add(1)
			}
			return queryloop.ToolPermission{Behavior: "allow", Input: in}, nil
		}
	})
	callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"actual-planned-team"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		future, err := parent.stageNativeTeamAgent(ctx, scope, "actual-planned-team", objMain(rawMain(map[string]string{"name": "planner", "prompt": "create and submit a physical plan then await the team lead", "mode": "plan"})), queryloop.JSONToolCallContext{})
		if err != nil {
			return err
		}
		_, err = future.Await(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-awaiting:
	case <-ctx.Done():
		t.Fatal("required plan producer never reached real tool consumer", ctx.Err())
	}
	if data, err := os.ReadFile(planPath); err != nil || string(data) != plan {
		t.Fatal("plan Write did not persist", string(data), err)
	}
	var taskID string
	var mailbox []protocol.JSONObject
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		tasks, err := parent.Controller.BackgroundTasksOnOwner(scope)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			identity, _ := task.Object("identity")
			if identity.String("agentName") == "planner" {
				taskID = task.String("id")
				if string(task.Get("awaitingPlanApproval")) != "true" || task.String("permissionMode") != "plan" {
					t.Fatal("Source required task flag/mode lost", string(rawMain(task)))
				}
			}
		}
		if taskID == "" {
			t.Fatal("actual spawned plan task missing")
		}
		if parent.teamLeader.mailbox == nil {
			_, _, err = parent.interactiveLeaderInbox(scope)
			if err != nil {
				return err
			}
		}
		_ = json.Unmarshal(parent.teamLeader.mailbox.Read("team-lead", "actual-planned-team"), &mailbox)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var request protocol.JSONObject
	for _, row := range mailbox {
		parsed := objMain([]byte(row.String("text")))
		if parsed.String("type") == "plan_approval_request" {
			request = parsed
		}
	}
	if request.String("planContent") != plan || request.String("planFilePath") != planPath || !strings.HasSuffix(request.String("requestId"), "@planner@actual-planned-team") {
		t.Fatal("actual mailbox approval request", string(rawMain(request)))
	}
	assertRequiredPlanSourceTool(t, parent, request, planPath, plan)
	if _, err := parent.PollInteractiveLeaderInbox(ctx); err != nil {
		t.Fatal(err)
	}
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		task, err := parent.Controller.BackgroundTaskOnOwner(scope, taskID)
		if err != nil {
			return err
		}
		if string(task.Data.Get("awaitingPlanApproval")) != "false" || task.Data.String("permissionMode") != "plan" {
			t.Fatal("Source leader helper only clears awaiting flag", string(rawMain(task.Data)))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	released.Store(true)
	close(release)
	select {
	case <-nextTurn:
	case <-ctx.Done():
		t.Fatal("real idle mailbox loop did not start next plan sidechain", ctx.Err())
	}
	if exitPrompts.Load() != 0 {
		t.Fatal("required teammate plan went through user tool permission UI", exitPrompts.Load())
	}
	if err := parent.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
func assertRequiredPlanSourceTool(t *testing.T, parent *MainPrint, request protocol.JSONObject, planPath, plan string) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-plan-approval.fixture.ts")
	leaf := strings.TrimSuffix(filepath.Base(planPath), ".md")
	slug, agentID, _ := strings.Cut(leaf, "-agent-")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CLAUDE_CODE_TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_PLAN_APPROVAL_FIXTURE=1", "CC_HAHA_AGENT_TEAMS_ENABLED=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "sessionId": parent.Controller.SessionID(), "rows": []map[string]any{{"identity": map[string]any{"agentId": "planner@actual-planned-team", "agentName": "planner", "teamName": "actual-planned-team", "parentSessionId": parent.Controller.SessionID(), "planModeRequired": true}, "agentId": agentID, "slug": slug, "plan": plan, "inProcess": true}}}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(out))
	}
	var source struct {
		Results []struct {
			Result, Mapped, Permission protocol.JSONObject
			Mode                       string
			Awaiting                   bool
			Mailbox                    []protocol.JSONObject
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Capabilities.Blocked) > 0 || len(source.Results) != 1 {
		t.Fatal(err, string(out))
	}
	r := source.Results[0]
	if r.Mode != "plan" || !r.Awaiting || r.Permission.String("behavior") != "allow" || string(r.Result.Get("awaitingLeaderApproval")) != "true" {
		t.Fatal("Source required-plan state", string(out))
	}
	var original protocol.JSONObject
	for _, row := range r.Mailbox {
		p := objMain([]byte(row.String("text")))
		if p.String("type") == "plan_approval_request" {
			original = p
		}
	}
	original = original.Clone()
	request = request.Clone()
	for _, field := range []string{"timestamp", "requestId", "planFilePath"} {
		original.Delete(field)
		request.Delete(field)
	}
	var a, b any
	_ = json.Unmarshal(rawMain(original), &a)
	_ = json.Unmarshal(rawMain(request), &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("Source original ExitPlanMode request body differs", a, b)
	}
}
