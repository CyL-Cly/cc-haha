package teamreader

import (
	"bytes"
	"context"
	"encoding/json"
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

type watchFixtureFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type watchFixtureStep struct {
	Files  []watchFixtureFile `json:"files,omitempty"`
	Remove []string           `json:"remove,omitempty"`
}

func watcherReader(t *testing.T) *Service {
	t.Helper()
	config := filepath.Join(t.TempDir(), ".claude")
	now := func() time.Time { return time.UnixMilli(20000) }
	tasks, e := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: now})
	if e != nil {
		t.Fatal(e)
	}
	mailbox, e := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(Options{ConfigDir: config, Tasks: tasks, Mailbox: mailbox, Now: now})
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestOriginalTeamWatcherActualFilesEventsPlanAndIncarnationMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	config := func(created int, lead string, active bool) string {
		return string(raw(map[string]any{"name": "watch-team", "createdAt": created, "leadSessionId": lead, "leadAgentId": "team-lead@watch-team", "members": []map[string]any{{"agentId": "team-lead@watch-team", "name": "team-lead", "isActive": false}, {"agentId": "reader@watch-team", "name": "reader", "agentType": "Explore", "isActive": active, "currentTask": "physical"}}}))
	}
	first := config(1000, "parent", true)
	identity := watchPlanIncarnation(obj([]byte(first)))
	plan := map[string]any{"schemaVersion": 1, "planId": "plan-native", "sessionId": "parent", "teamName": "watch-team", "incarnationId": identity, "revision": 1, "state": "draft", "workDir": "/fixture", "leaderRuntime": map[string]string{"providerId": "fixture", "modelId": "fixture"}, "members": []any{}, "tasks": []any{}, "createdAt": 1000, "updatedAt": 1000}
	p1 := string(raw(plan))
	plan["revision"] = 2
	plan["state"] = "review_pending"
	p2 := string(raw(plan))
	plan["incarnationId"] = "stale"
	pStale := string(raw(plan))
	f := func(path, content string) watchFixtureFile { return watchFixtureFile{path, content} }
	steps := []watchFixtureStep{
		{},
		{Files: []watchFixtureFile{f(".claude/teams/watch-team/config.json", first)}},
		{},
		{Files: []watchFixtureFile{f(".claude/teams/watch-team/plan.json", p1)}},
		{Files: []watchFixtureFile{f(".claude/teams/watch-team/plan.json", p2)}},
		{Files: []watchFixtureFile{f(".claude/teams/watch-team/plan.json", pStale)}},
		{Files: []watchFixtureFile{f(".claude/teams/watch-team/inboxes/orphan.json", "[]"), f(".claude/projects/project/parent/subagents/agent-x.jsonl", `{"agentName":"legacy"}`+"\n"), f(".claude/projects/project/parent/subagents/agent-m.jsonl", `{"agentId":"fallback@watch-team"}`+"\n"), f(".claude/projects/project/parent/subagents/agent-m.meta.json", `{"agentType":"metadata"}`), f(".claude/teams/watch-team/config.json", config(1000, "parent", false))}},
		{Files: []watchFixtureFile{f(".claude/tasks/watch-team/1.json", `{"id":"1","subject":"physical task","description":"","status":"pending","blocks":[],"blockedBy":[]}`)}},
		{Files: []watchFixtureFile{f(".claude/teams/watch-team/inboxes/team-lead.json", `[{"from":"reader","text":"physical","timestamp":"1970-01-01T00:00:01Z","read":false}]`)}},
		{Files: []watchFixtureFile{f(".claude/teams/watch-team/config.json", `{"name":"watch-team","members":[{"agentId":"reader@watch-team","name":"reader","isActive":false},`)}},
		{Files: []watchFixtureFile{f(".claude/teams/watch-team/config.json", config(2000, "new-parent", true))}},
		{Remove: []string{".claude/teams/watch-team"}},
		{Files: []watchFixtureFile{f(".claude/teams/watch-team/config.json", config(3000, "third-parent", true))}},
		{Remove: []string{".claude/teams"}},
		{},
	}
	for _, value := range []string{`{"name":"watch-team","createdAt":null,"members":[]}`, `{"name":"watch-team","createdAt":" 0x10 ","members":[]}`, `{"name":"watch-team","createdAt":true,"members":[]}`, `{"name":"watch-team","createdAt":[4],"members":[]}`, `{"name":"watch-team","createdAt":[],"members":[]}`, `{"name":"watch-team","createdAt":{},"members":[]}`, `{"name":"watch-team","createdAt":{"valueOf":1,"toString":2},"members":[]}`, `{"name":"watch-team","createdAt":1,"members":[1,true,"text",[]]}`, `{"name":"watch-team","createdAt":"Infinity","members":[]}`, `{"name":"watch-team","members":null}`, `{"name":"watch-team","members":[null]}`, `7`, `[]`, `null`, `{"name":"watch-team","createdAt":1000,"members":[{"agentId":12,"name":false,"agentType":"fallback","currentTask":{"id":1},"isActive":"false"}]}`} {
		steps = append(steps, watchFixtureStep{Files: []watchFixtureFile{f(".claude/teams/watch-team/config.json", value)}})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-watcher.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_WATCHER_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "USER_TYPE=external", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "steps": steps}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows         [][]json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Rows) != len(steps) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(output))
	}
	s := watcherReader(t)
	deliveries := []protocol.JSONObject{}
	w, e := NewWatcher(s, func(message protocol.JSONObject, lead string) {
		var target any
		if lead != "" {
			target = lead
		}
		deliveries = append(deliveries, protocol.NewJSONObject(field("message", message), field("leadSessionId", target)))
	})
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close(context.Background())
	for i, step := range steps {
		for _, file := range step.Files {
			path := filepath.Join(filepath.Dir(s.options.ConfigDir), file.Path)
			if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(path, []byte(file.Content), 0600); e != nil {
				t.Fatal(e)
			}
		}
		for _, name := range step.Remove {
			if e = os.RemoveAll(filepath.Join(filepath.Dir(s.options.ConfigDir), name)); e != nil {
				t.Fatal(e)
			}
		}
		if e = w.CheckNow(context.Background()); e != nil {
			t.Fatal(e)
		}
		var expected, actual any
		_ = json.Unmarshal(raw(source.Rows[i]), &expected)
		_ = json.Unmarshal(raw(deliveries), &actual)
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("step%d Go %s Source %s", i, raw(deliveries), raw(source.Rows[i]))
		}
		deliveries = []protocol.JSONObject{}
	}
}
