package teamworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func launchPlan(name, home string) protocol.JSONObject {
	member := func(id, name string) protocol.JSONObject {
		return validationObject(map[string]any{"id": id, "name": name, "agentType": "approved", "prompt": "Actual approved task for " + name, "providerName": "Private", "runtime": map[string]string{"providerId": "private", "modelId": "concrete-model", "effortLevel": "high"}, "agentSnapshot": map[string]any{"source": "built-in", "sourceIdentity": map[string]string{"kind": "builtin"}, "systemPrompt": "Actual approved frozen system", "initialPrompt": "Original initial instruction", "agentType": "approved", "tools": []string{"Read", "TaskGet"}}})
	}
	members := []protocol.JSONObject{member("member-one", "worker-one"), member("member-two", "worker-two")}
	tasks := []protocol.JSONObject{validationObject(map[string]any{"id": "proposal-a", "subject": "Actual first task", "description": "first", "ownerId": "member-one", "dependencies": []string{}}), validationObject(map[string]any{"id": "proposal-b", "subject": "Actual second task", "ownerId": "member-two", "dependencies": []string{"proposal-a"}})}
	return validationObject(map[string]any{"schemaVersion": 1, "planId": "11111111-1111-4111-8111-111111111111", "sessionId": "22222222-2222-4222-8222-222222222222", "teamName": name, "incarnationId": "pending", "revision": 3, "state": "launching", "workDir": home, "leaderRuntime": map[string]string{"providerId": "private", "modelId": "concrete-model"}, "members": members, "tasks": tasks, "createdAt": 1000, "updatedAt": 2000, "approvedSnapshot": map[string]any{"revision": 2, "requestId": "human-approval", "approvedAt": 2000, "members": members, "tasks": tasks, "leaderRuntime": map[string]string{"providerId": "private", "modelId": "concrete-model"}}, "launch": map[string]string{"status": "pending"}})
}
func TestActualTeamRuntimeDurableBarrierSharedTasksMailboxAndRollbackMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	type testCase struct {
		Plan        protocol.JSONObject `json:"plan"`
		FailPrepare int                 `json:"failPrepare,omitempty"`
		FailRelease int                 `json:"failRelease,omitempty"`
		Idle        string              `json:"idle,omitempty"`
		Inbox       bool                `json:"inbox,omitempty"`
		Interrupted bool                `json:"interrupted,omitempty"`
	}
	cases := []testCase{{Plan: launchPlan("launch-ready", home)}, {Plan: launchPlan("launch-prepare-failed", home), FailPrepare: 2}, {Plan: launchPlan("launch-release-failed", home), FailRelease: 2}, {Plan: launchPlan("launch-idle", home), Idle: "available"}, {Plan: launchPlan("launch-inbox", home), Inbox: true}, {Plan: launchPlan("launch-interrupted", home), Interrupted: true}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-runtime-launch.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_LAUNCH_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": cases}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	for i, c := range cases {
		t.Run(c.Plan.String("teamName"), func(t *testing.T) {
			configDir := filepath.Join(t.TempDir(), ".claude")
			now := func() time.Time { return time.UnixMilli(2000) }
			tasks, _ := taskstore.New(taskstore.Options{ConfigDir: configDir, PID: os.Getpid(), Now: now})
			config, _ := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: configDir, TaskStore: tasks, PID: os.Getpid()})
			plans, _ := teamplan.New(teamplan.Options{Config: config, Tasks: tasks, ConfigDir: configDir, WorkDir: home, Now: now, UUID: func() (string, error) { return "private", nil }})
			mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: configDir, TaskStore: tasks, NewID: func() (string, error) { return "mailbox-private", nil }})
			plan := c.Plan.Clone()
			teamName, parent := plan.String("teamName"), plan.String("sessionId")
			team := validationObject(map[string]any{"name": teamName, "leadSessionId": parent, "leadAgentId": "team-lead@" + teamName, "createdAt": 1000, "members": []any{}, "future": map[string]bool{"keep": true}})
			if err = config.Write(teamName, team); err != nil {
				t.Fatal(err)
			}
			inc, _ := plans.Incarnation(teamName, parent)
			plan.Set("incarnationId", raw(inc))
			if err = os.WriteFile(plans.Path(teamName), raw(plan), 0600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			calls := []any{}
			children := []string{}
			prepare, releases := []protocol.JSONObject{}, []protocol.JSONObject{}
			connected := map[string]bool{parent: true}
			observers := map[string]func(protocol.JSONObject){}
			record := func(v any) { mu.Lock(); calls = append(calls, v); mu.Unlock() }
			index := func(id string) int {
				for i, c := range children {
					if id == c {
						return i + 1
					}
				}
				return 0
			}
			counter := 0
			o := RuntimeOptions{Config: config, Plans: plans, Tasks: tasks, Mailbox: mailbox, Now: now, UUID: func() (string, error) {
				counter++
				return "33333333-3333-4333-8333-" + string(rune('0'+counter)) + "00000000000", nil
			}, Connected: func(id string) bool { mu.Lock(); defer mu.Unlock(); return connected[id] }, WaitWorkersStopped: func(context.Context, string) error { record([]any{"wait"}); return nil }, Validation: ValidationOptions{PermissionMode: func(context.Context, string) (string, error) { return "default", nil }, Routing: func(_ context.Context, v protocol.JSONObject, _ string) (protocol.JSONObject, string, error) {
				return v, "Private", nil
			}}, Log: func(err error) { t.Error(err) }}
			o.Prepare = func(_ context.Context, w WorkerRequest) (string, error) {
				mu.Lock()
				children = append(children, w.ID)
				n := len(children)
				options := validationObject(map[string]any{"providerId": w.ProviderID, "model": w.Model, "effort": w.Effort, "permissionMode": w.PermissionMode, "teamWorker": map[string]any{"parentSessionId": w.Worker.ParentSessionID, "teamName": w.Worker.TeamName, "memberId": w.Worker.MemberID, "name": w.Worker.Name, "systemPrompt": w.Worker.SystemPrompt, "tools": w.Worker.Tools, "agentDefinition": w.Worker.AgentDefinition}})
				prepare = append(prepare, validationObject(map[string]any{"id": w.ID, "workDir": w.WorkDir, "options": options}))
				calls = append(calls, []any{"prepare", n})
				if c.FailPrepare == n {
					mu.Unlock()
					return "", errors.New("actual prepare failure")
				}
				connected[w.ID] = true
				calls = append(calls, []any{"model", n, map[string]string{"subtype": "set_model", "model": w.Model}})
				mu.Unlock()
				return w.ID, nil
			}
			o.Release = func(_ context.Context, id, text string, canSend func() bool) (bool, error) {
				mu.Lock()
				defer mu.Unlock()
				releases = append(releases, validationObject(map[string]string{"id": id, "text": text}))
				var allowed any
				if canSend != nil {
					allowed = canSend()
				}
				calls = append(calls, []any{"release", index(id), text, allowed})
				return c.FailRelease != len(releases), nil
			}
			o.StopWorker = func(_ context.Context, id string) error {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, []any{"stop", index(id)})
				delete(connected, id)
				return nil
			}
			o.Snapshot = func(_ context.Context, id, name string, created json.RawMessage) error {
				record([]any{"snapshot", validationObject(map[string]any{"subtype": "team_runtime_snapshot", "team_name": name, "created_at": created})})
				return nil
			}
			o.Observe = func(id string, cb func(protocol.JSONObject)) (func(), error) {
				observers[id] = cb
				return func() { delete(observers, id) }, nil
			}
			o.SendLeader = func(_ context.Context, id, text string) (bool, error) {
				record([]any{"leader", text})
				return true, nil
			}
			runtime, err := NewRuntime(o)
			if err != nil {
				t.Fatal(err)
			}
			result, failure := runtime.Launch(ctx, plan)
			if failure == nil {
				if c.Idle != "" {
					observers[children[0]](validationObject(map[string]any{"type": "result", "is_error": false, "result": "actual output " + string(bytes.Repeat([]byte("x"), 1010))}))
					deadline := time.Now().Add(2 * time.Second)
					for len(rows(mailbox.Read("team-lead", teamName))) == 0 {
						if time.Now().After(deadline) {
							t.Fatal("actual idle callback did not write its notification")
						}
						time.Sleep(time.Millisecond)
					}
				}
				if c.Inbox {
					_ = mailbox.Write("worker-one", teamName, validationObject(map[string]string{"from": "worker-two", "text": "actual inbox", "timestamp": now().UTC().Format("2006-01-02T15:04:05.000Z")}))
					_ = mailbox.Write("worker-one", teamName, validationObject(map[string]string{"from": "legacy", "text": "legacy inbox", "timestamp": now().UTC().Format("2006-01-02T15:04:05.000Z")}))
					values := rows(mailbox.Read("worker-one", teamName))
					values[1].Delete("id")
					_ = os.WriteFile(mailbox.Path("worker-one", teamName), raw(values), 0600)
					if err = runtime.Poll(ctx, plan.String("planId"), rows(object(plan.Get("approvedSnapshot")).Get("members")), object(result.Get("memberIds")), raw(1000)); err != nil {
						t.Fatal(err)
					}
				}
				if c.Interrupted {
					snapshot, _ := tasks.Snapshot(teamstore.SanitizeName(teamName))
					_, _ = tasks.Update(teamstore.SanitizeName(teamName), snapshot.Tasks[0].Get("id"), validationObject(map[string]string{"status": "in_progress"}))
				}
				if err = runtime.Stop(ctx, plan.String("planId")); err != nil {
					t.Fatal(err)
				}
			}
			if err = runtime.Close(ctx); err != nil {
				t.Fatal(err)
			}
			var message any
			started := false
			var interrupted interface{ ExecutionStarted() bool }
			if failure != nil {
				message = failure.Error()
				if errors.As(failure, &interrupted) {
					started = interrupted.ExecutionStarted()
				}
			}
			var resultValue any
			if failure == nil {
				resultValue = result
			}
			snapshot, err := tasks.Snapshot(teamstore.SanitizeName(teamName))
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			actual := raw(map[string]any{"result": resultValue, "error": message, "executionStarted": started, "calls": calls, "prepare": prepare, "releases": releases, "config": config.Read(teamName), "tasks": snapshot.Tasks, "inbox": json.RawMessage(mailbox.Read("worker-one", teamName)), "leaderInbox": json.RawMessage(mailbox.Read("team-lead", teamName))})
			mu.Unlock()
			var normalize func(any) any
			normalize = func(v any) any {
				switch value := v.(type) {
				case string:
					for i, c := range children {
						if value == c {
							return "<worker-" + string(rune('1'+i)) + ">"
						}
					}
					if strings.HasPrefix(value, "mailbox-") {
						return "<mailbox-id>"
					}
					return value
				case []any:
					for i, item := range value {
						value[i] = normalize(item)
					}
				case map[string]any:
					for key, item := range value {
						value[key] = normalize(item)
					}
				}
				return v
			}
			var a, b any
			_ = json.Unmarshal(actual, &a)
			a = normalize(a)
			_ = json.Unmarshal(source.Rows[i], &b)
			// Source Promise.allSettled and the native parallel stops may
			// complete in either order. Preserve the ordered barrier/release
			// observations and compare the full stop multiset separately.
			stripParallelStops := func(value any) {
				v := value.(map[string]any)
				events := v["calls"].([]any)
				ordered := []any{}
				stops := []string{}
				for _, event := range events {
					row := event.([]any)
					if row[0] == "stop" {
						stops = append(stops, string(raw(row)))
					} else {
						ordered = append(ordered, event)
					}
				}
				sort.Strings(stops)
				v["calls"] = ordered
				v["parallelStops"] = stops
			}
			stripParallelStops(a)
			stripParallelStops(b)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("Go%s\nSource%s", raw(a), source.Rows[i])
			}
		})
	}
}
