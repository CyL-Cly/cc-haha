package teamreader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActualTeamListDetailDiscoveryActivityAndUserMailboxMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type file struct {
		Path, Content string
		Mtime         *int64
	}
	mtime := func(n int64) *int64 { return &n }
	files := []file{
		{".claude/teams/team/config.json", `{"name":"team","description":"Actual roster","createdAt":1000,"leadAgentId":"team-lead@team","leadSessionId":"parent","members":[{"agentId":"team-lead@team","name":"team-lead","agentType":"leader","model":"model","joinedAt":1000,"cwd":"$HOME/project","isActive":false},{"agentId":"busy@team","name":"busy","agentType":"worker","providerId":null,"providerName":"Official","effortLevel":"low","color":"blue","backendType":"process","joinedAt":1000,"cwd":"$HOME/project","sessionId":"child","isActive":true},{"agentId":"legacy@team","name":"legacy","joinedAt":1000,"cwd":"$HOME/project"},{"agentId":"failed@team","name":"failed","isActive":false,"terminated":true,"lastError":"authentication 401"},{"agentId":"retry@team","name":"retry","isActive":false,"lastError":"network timeout","autoRetry":{"attempt":1,"max":5,"nextAt":25000,"unknown":"ignored"}},{"agentId":"invalid-retry@team","name":"invalid-retry","isActive":false,"lastError":"failure","autoRetry":{"attempt":"1","max":5,"nextAt":25000}}]}`, nil},
		{".claude/teams/team/inboxes/team-lead.json", `[]`, nil}, {".claude/teams/team/inboxes/inbox-only.json", `[]`, nil}, {".claude/teams/team/inboxes/legacy.json", `[]`, nil},
		{".claude/teams/bad/config.json", `bad JSON`, nil}, {".claude/teams/wrong/config.json", `{"name":"wrong","members":{}}`, nil},
		{".claude/teams/second/config.json", `{"name":"second","createdAt":2000,"members":[]}`, nil},
		{".claude/projects/proj/parent/subagents/agent-1.jsonl", `{"agentName":"legacy"}` + "\n", mtime(5000)},
		{".claude/projects/proj/parent/subagents/agent-2.jsonl", `{"agentName":"wrong-name"}` + "\n", mtime(19000)},
		{".claude/projects/proj/parent/subagents/agent-2.meta.json", `{"agentType":"meta-member"}`, nil},
		{".claude/projects/other/parent/subagents/agent-3.jsonl", `invalid` + "\n" + `{"agentId":"head-member@team"}` + "\n", mtime(19000)},
		{".claude/projects/other/parent/subagents/agent-4.jsonl", `{"agentName":"legacy"}` + "\n", mtime(18000)},
		{".claude/projects/proj/parent/subagents/agent-5.jsonl", `{"agentName":"team-lead"}` + "\n", mtime(19000)},
	}
	rows := []protocol.JSONObject{obj([]byte(`{"method":"list"}`)), obj([]byte(`{"method":"get","name":"team"}`)), obj([]byte(`{"method":"get","name":"missing"}`)), obj([]byte(`{"method":"get","name":"wrong"}`)), obj([]byte(`{"method":"send","name":"team","id":"busy@team","content":"  actual message  ","inbox":".claude/teams/team/inboxes/busy.json"}`)), obj([]byte(`{"method":"send","name":"team","id":"inbox-only@team","content":"message to discovered inbox"}`)), obj([]byte(`{"method":"send","name":"team","id":"head-member@team","content":"message to discovered transcript"}`)), obj([]byte(`{"method":"send","name":"team","id":"absent","content":"present"}`)), obj([]byte(`{"method":"send","name":"team","id":"busy","content":" \ufeff "}`)), obj([]byte(`{"method":"send","name":"team","id":"busy","content":true}`)), obj([]byte(`{"method":"get","name":"team"}`)), obj([]byte(`{"method":"list"}`))}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-reader-basic.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_READER_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	// Struct field JSON names match the physical Source fixture explicitly.
	seed := []map[string]any{}
	for _, file := range files {
		v := map[string]any{"path": file.Path, "content": file.Content}
		if file.Mtime != nil {
			v["mtime"] = *file.Mtime
		}
		seed = append(seed, v)
	}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "files": seed, "rows": rows, "now": 20000}))
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
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	goHome := t.TempDir()
	for _, file := range files {
		path := filepath.Join(goHome, file.Path)
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		if err = os.WriteFile(path, []byte(strings.ReplaceAll(file.Content, "$HOME", goHome)), 0600); err != nil {
			t.Fatal(err)
		}
		if file.Mtime != nil {
			if err = os.Chtimes(path, time.UnixMilli(*file.Mtime), time.UnixMilli(*file.Mtime)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: filepath.Join(goHome, ".claude"), PID: os.Getpid(), Now: func() time.Time { return time.UnixMilli(20000) }})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: filepath.Join(goHome, ".claude"), TaskStore: tasks, NewID: func() (string, error) { return "mailbox-fixture-reader-id", nil }})
	reader, _ := New(Options{ConfigDir: filepath.Join(goHome, ".claude"), Tasks: tasks, Mailbox: mailbox, Now: func() time.Time { return time.UnixMilli(20000) }})
	for i, row := range rows {
		var data any
		var failure error
		switch row.String("method") {
		case "list":
			data, failure = reader.ListTeams(context.Background())
		case "get":
			data, failure = reader.GetTeam(context.Background(), row.String("name"))
		case "send":
			failure = reader.SendMemberMessage(context.Background(), row.String("name"), row.String("id"), row.Get("content"))
			if failure == nil {
				data = map[string]bool{"ok": true}
			}
		}
		status := 200
		var apiError any
		if failure != nil {
			data = nil
			status = 500
			apiError = map[string]string{"code": "INTERNAL_ERROR", "message": "An unexpected error occurred"}
			var api *teamsapi.APIError
			if errors.As(failure, &api) {
				status = api.Status
				apiError = map[string]string{"code": api.Code, "message": api.Message}
			}
		}
		var inbox any
		if path := row.String("inbox"); path != "" {
			if b, err := os.ReadFile(filepath.Join(goHome, path)); err == nil {
				_ = json.Unmarshal(b, &inbox)
			}
		}
		actual := raw(map[string]any{"data": data, "status": status, "error": apiError, "inbox": inbox})
		expected := source.Rows[i]
		actual = []byte(strings.ReplaceAll(string(actual), goHome, "$HOME"))
		expected = []byte(strings.ReplaceAll(string(expected), home, "$HOME"))
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(expected, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("row %d Go %s Source %s", i, actual, expected)
		}
	}
}
