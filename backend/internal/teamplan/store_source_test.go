package teamplan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func TestTeamPlanDurableDraftHumanRoutingReviewApprovalAndArchiveMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	plan := object(fixturePlan())
	runtime := object(plan.Get("leaderRuntime"))
	initial := protocol.NewJSONObject(protocol.JSONField{Name: "members", Value: plan.Get("members")}, protocol.JSONField{Name: "tasks", Value: plan.Get("tasks")}, protocol.JSONField{Name: "agentCatalog", Value: plan.Get("agentCatalog")})
	var members []protocol.JSONObject
	_ = json.Unmarshal(plan.Get("members"), &members)
	member := members[0].Clone()
	newRuntime := object(member.Get("runtime"))
	newRuntime.Set("modelId", raw("model-suggestion"))
	member.Set("runtime", raw(newRuntime))
	actions := []protocol.JSONObject{}
	action := func(typ string, fields ...protocol.JSONField) protocol.JSONObject {
		return protocol.NewJSONObject(append([]protocol.JSONField{{Name: "type", Value: raw(typ)}}, fields...)...)
	}
	actions = append(actions, action("ensure", protocol.JSONField{Name: "runtime", Value: raw(runtime)}, protocol.JSONField{Name: "initial", Value: raw(initial)}), action("ensure", protocol.JSONField{Name: "runtime", Value: raw(runtime)}, protocol.JSONField{Name: "initial", Value: raw(initial)}), action("replace", protocol.JSONField{Name: "stale", Value: raw(true)}, protocol.JSONField{Name: "patch", Value: []byte(`{}`)}), action("stage", protocol.JSONField{Name: "member", Value: raw(member)}), action("submit"), action("replace", protocol.JSONField{Name: "preserveReview", Value: raw(true)}, protocol.JSONField{Name: "patch", Value: raw(map[string]any{"feedback": "human review", "future": true})}), action("approve", protocol.JSONField{Name: "requestId", Value: raw("human-approval")}), action("approve", protocol.JSONField{Name: "requestId", Value: raw("human-approval")}), action("approved", protocol.JSONField{Name: "member", Value: raw("研究员_1")}), action("mutate", protocol.JSONField{Name: "patch", Value: []byte(`{"state":"running"}`)}), action("stage", protocol.JSONField{Name: "member", Value: raw(member)}))
	newMember := member.Clone()
	newMember.Set("id", raw("second"))
	newMember.Set("name", raw("second"))
	actions = append(actions, action("stage", protocol.JSONField{Name: "member", Value: raw(newMember)}), action("approved", protocol.JSONField{Name: "member", Value: raw("研究员_1")}), action("approved", protocol.JSONField{Name: "member", Value: raw("second")}), action("find"))
	home := t.TempDir()
	for _, dir := range []string{".claude", "tmp"} {
		_ = os.MkdirAll(filepath.Join(home, dir), 0700)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-plan-store.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_PLAN_STORE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "actions": actions}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(actions) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	nativeHome := t.TempDir()
	configDir := filepath.Join(nativeHome, ".claude")
	tasks, e := taskstore.New(taskstore.Options{ConfigDir: configDir, PID: os.Getpid(), Now: func() time.Time { return time.UnixMilli(2000) }})
	if e != nil {
		t.Fatal(e)
	}
	config, e := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: configDir, TaskStore: tasks, PID: os.Getpid()})
	if e != nil {
		t.Fatal(e)
	}
	if e = config.Write("source-team", object([]byte(`{"name":"source-team","members":[],"leadSessionId":"source-session","createdAt":1000,"future":true}`))); e != nil {
		t.Fatal(e)
	}
	count := 0
	store, e := New(Options{Config: config, Tasks: tasks, ConfigDir: configDir, WorkDir: home, Now: func() time.Time { return time.UnixMilli(2000) }, UUID: func() (string, error) { count++; return fmt.Sprintf("00000000-0000-4000-8000-%012d", count), nil }})
	if e != nil {
		t.Fatal(e)
	}
	for i, a := range actions {
		current, e := store.Read("source-team")
		if e != nil {
			t.Fatal(e)
		}
		id := Identity{}
		if current != nil {
			id = CurrentIdentity(*current)
		}
		if string(a.Get("stale")) == "true" {
			id.ExpectedRevision = 0
		}
		var result any
		var err error
		switch a.String("type") {
		case "ensure":
			input := object(a.Get("initial"))
			input.Set("workDir", raw(home))
			result, err = store.EnsureDraft("source-team", "source-session", object(a.Get("runtime")), input)
		case "replace":
			result, err = store.Replace("source-team", id, object(a.Get("patch")), string(a.Get("preserveReview")) == "true")
		case "submit":
			var patch *protocol.JSONObject
			if a.Get("patch") != nil {
				v := object(a.Get("patch"))
				patch = &v
			}
			result, err = store.Submit("source-team", id, patch)
		case "approve":
			var p protocol.JSONObject
			var committed bool
			p, committed, err = store.Approve("source-team", id, a.String("requestId"), *current)
			if err == nil {
				result = map[string]any{"plan": p, "committed": committed}
			}
		case "mutate":
			result, err = store.Mutate("source-team", id, func(v protocol.JSONObject) (protocol.JSONObject, error) {
				v.Assign(object(a.Get("patch")))
				return v, nil
			})
		case "stage":
			result, err = store.StageMember("source-team", object(a.Get("member")))
		case "approved":
			result, err = store.IsApproved("source-team", a.String("member"))
		case "find":
			result, err = store.FindForSession("source-session")
		}
		var message, status any
		if err != nil {
			message = err.Error()
			var planErr *Error
			if errors.As(err, &planErr) {
				status = planErr.Status
			}
			result = nil
		}
		var physical any
		if data, e := os.ReadFile(store.Path("source-team")); e == nil {
			physical = json.RawMessage(data)
		}
		actual := raw(map[string]any{"result": result, "error": message, "status": status, "physical": physical})
		var x, y any
		_ = json.Unmarshal(actual, &x)
		_ = json.Unmarshal(source.Rows[i], &y)
		if !reflect.DeepEqual(x, y) {
			t.Fatalf("step%d %s\n%s", i, actual, source.Rows[i])
		}
	}
}
