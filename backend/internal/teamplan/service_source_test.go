package teamplan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

type startedError struct{ error }

func (startedError) ExecutionStarted() bool { return true }
func TestTeamPlanServiceReviewLaunchFailureDeliveryAndRestartPolicyMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	kinds := []string{"success", "invalid", "failed", "started", "notify", "lost_running", "lost_launching"}
	base := object(fixturePlan())
	initial := protocol.NewJSONObject(protocol.JSONField{Name: "members", Value: base.Get("members")}, protocol.JSONField{Name: "tasks", Value: base.Get("tasks")})
	rows := []map[string]any{}
	for _, kind := range kinds {
		rows = append(rows, map[string]any{"kind": kind, "runtime": base.Get("leaderRuntime"), "initial": initial})
	}
	home := t.TempDir()
	for _, dir := range []string{".claude", "tmp"} {
		_ = os.MkdirAll(filepath.Join(home, dir), 0700)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-plan-service.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_PLAN_SERVICE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": rows}))
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
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(kinds) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	for i, kind := range kinds {
		nativeHome := t.TempDir()
		configDir := filepath.Join(nativeHome, ".claude")
		tasks, _ := taskstore.New(taskstore.Options{ConfigDir: configDir, PID: os.Getpid(), Now: time.Now})
		config, _ := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: configDir, TaskStore: tasks, PID: os.Getpid()})
		team := "policy-" + kind
		_ = config.Write(team, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw(team)}, protocol.JSONField{Name: "members", Value: []byte(`[]`)}, protocol.JSONField{Name: "leadSessionId", Value: raw(team)}, protocol.JSONField{Name: "createdAt", Value: raw(1000)}))
		store, _ := New(Options{Config: config, Tasks: tasks, ConfigDir: configDir, WorkDir: home, Now: func() time.Time { return time.UnixMilli(2000) }, UUID: func() (string, error) { return "00000000-0000-4000-8000-000000000001", nil }})
		input := initial.Clone()
		input.Set("workDir", raw(home))
		plan, e := store.EnsureDraft(team, team, object(base.Get("leaderRuntime")), input)
		if e != nil {
			t.Fatal(e)
		}
		plan, e = store.Submit(team, CurrentIdentity(plan), nil)
		if e != nil {
			t.Fatal(e)
		}
		calls := []string{}
		var mu sync.Mutex
		record := func(value string) { mu.Lock(); calls = append(calls, value); mu.Unlock() }
		service, e := NewService(store, Runtime{Validate: func(_ context.Context, v protocol.JSONObject) (protocol.JSONObject, error) {
			record("validate")
			if kind == "invalid" {
				return v, errors.New("actual validation rejected")
			}
			return v, ValidateGraph(v, true)
		}, Launch: func(context.Context, protocol.JSONObject) (protocol.JSONObject, error) {
			record("launch")
			if kind == "failed" || kind == "started" {
				e := errors.New("actual launch rejected")
				if kind == "started" {
					return protocol.JSONObject{}, startedError{e}
				}
				return protocol.JSONObject{}, e
			}
			return protocol.NewJSONObject(protocol.JSONField{Name: "memberIds", Value: []byte(`{"member":"private-owned-worker-session"}`)}), nil
		}, Stop: func(context.Context, string) error { record("stop"); return nil }, IsRunning: func(context.Context, string) (bool, error) { return kind != "lost_running", nil }, NotifyLeader: func(context.Context, protocol.JSONObject, string) error {
			record("notify")
			if kind == "notify" {
				return errors.New("actual delivery failed")
			}
			return nil
		}})
		if e != nil {
			t.Fatal(e)
		}
		action := func(plan protocol.JSONObject) protocol.JSONObject {
			v := plan.Clone()
			v.Set("expectedRevision", plan.Get("revision"))
			v.Set("requestId", raw("human-request"))
			return v
		}
		var err error
		if kind == "notify" {
			_, err = service.Action(ctx, team, "return", action(plan))
		} else {
			var approved protocol.JSONObject
			approved, err = service.Approve(ctx, team, action(plan))
			service.wg.Wait()
			if err == nil {
				if kind == "lost_launching" {
					current, _ := store.Read(team)
					_, err = store.Mutate(team, CurrentIdentity(*current), func(v protocol.JSONObject) (protocol.JSONObject, error) {
						v.Set("state", raw("launching"))
						return v, nil
					})
				}
				if kind == "lost_running" || kind == "lost_launching" {
					_, err = service.GetForSession(ctx, team)
				}
				if kind == "failed" {
					current, _ := store.Read(team)
					_, err = service.Action(ctx, team, "retry", action(*current))
				}
				_ = approved
			}
		}
		current, e := store.Read(team)
		if e != nil {
			t.Fatal(e)
		}
		var message, status any
		if err != nil {
			message = err.Error()
			var typed *Error
			if errors.As(err, &typed) {
				status = typed.Status
			}
		}
		var normalize func(any) any
		normalize = func(value any) any {
			switch v := value.(type) {
			case map[string]any:
				for key, item := range v {
					if key == "planId" || key == "incarnationId" {
						v[key] = "<" + key + ">"
					} else {
						v[key] = normalize(item)
					}
				}
			case []any:
				for i, item := range v {
					v[i] = normalize(item)
				}
			}
			return value
		}
		var value any
		_ = json.Unmarshal(raw(current), &value)
		mu.Lock()
		actual := raw(map[string]any{"plan": normalize(value), "calls": calls, "error": message, "status": status})
		mu.Unlock()
		_ = service.Close(ctx)
		var x, y any
		_ = json.Unmarshal(actual, &x)
		_ = json.Unmarshal(source.Rows[i], &y)
		if !reflect.DeepEqual(x, y) {
			t.Fatalf("%s %s\n%s", kind, actual, source.Rows[i])
		}
	}
}
