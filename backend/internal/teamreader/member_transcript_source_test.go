package teamreader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func TestActualMemberTranscriptRootFragmentsStandaloneArchiveIncarnationAndMissingMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	put := func(path, content string) {
		t.Helper()
		path = filepath.Join(config, path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	created := float64(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC).UnixMilli())
	team := protocol.NewJSONObject(field("name", "team"), field("createdAt", created), field("leadSessionId", "lead"), field("leadAgentId", "team-lead@team"), field("members", []protocol.JSONObject{protocol.NewJSONObject(field("agentId", "root@team"), field("name", "root"), field("sessionId", "root")), protocol.NewJSONObject(field("agentId", "fragments@team"), field("name", "fragments")), protocol.NewJSONObject(field("agentId", "standalone@team"), field("name", "standalone")), protocol.NewJSONObject(field("agentId", "empty@team"), field("name", "empty"))}))
	put("teams/team/config.json", string(raw(team)))
	entry := func(id, agent, text, stamp string) string {
		return string(raw(protocol.NewJSONObject(field("type", "assistant"), field("uuid", id), field("agentName", agent), field("teamName", "team"), field("timestamp", stamp), field("message", protocol.NewJSONObject(field("content", text)))))) + "\n"
	}
	put("projects/p/root.jsonl", entry("root-entry", "root", "root execution", "2026-10-07T00:00:01.000Z"))
	first := entry("first", "fragments", "earlier execution", "2026-10-07T00:00:02.000Z")
	put("projects/p/lead/subagents/agent-a.jsonl", first)
	put("projects/p/lead/subagents/agent-b.jsonl", first+entry("second", "fragments", "continuation", "2026-10-07T00:00:03.000Z"))
	put("projects/p/lead/subagents/agent-old.jsonl", entry("old", "fragments", "old incarnation", "2026-10-06T00:00:00.000Z"))
	put("projects/p/lead/subagents/agent-unrelated.jsonl", entry("other", "unrelated", "mentions fragments", "2026-10-07T00:00:02.000Z"))
	put("projects/p/standalone-one.jsonl", entry("standalone-first", "standalone", "standalone execution", "2026-10-07T00:00:04.000Z"))
	put("projects/other/standalone-two.jsonl", entry("standalone-next", "standalone", "standalone continuation", "2026-10-07T00:00:05.000Z"))
	put("teams/team/inboxes/discovered.json", "[]")
	put("projects/p/lead/subagents/agent-discovered.jsonl", entry("discovered", "discovered", "inbox discovery", "2026-10-07T00:00:06.000Z"))
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: config, Tasks: tasks, Mailbox: mailbox, Now: time.Now})
	oldTeam := team.Clone()
	oldTeam.Set("createdAt", raw(created-86400000))
	oldTeam.Set("members", raw([]protocol.JSONObject{protocol.NewJSONObject(field("agentId", "historical@team"), field("name", "historical"), field("sessionId", "historical"))}))
	oldTeam.Set("incarnationId", raw(incarnation(oldTeam)))
	archive := protocol.NewJSONObject(field("version", "old"), field("generatedAt", "2026-10-06T00:00:08.000Z"), field("deletedAt", "2026-10-06T00:00:10.000Z"), field("team", oldTeam), field("tasks", []any{}), field("messages", []any{}))
	if err := reader.ArchiveSnapshot(context.Background(), archive); err != nil {
		t.Fatal(err)
	}
	put("projects/p/historical.jsonl", entry("historical", "historical", "real archived execution", "2026-10-06T00:00:03.000Z"))
	rowsInput := []protocol.JSONObject{}
	for _, id := range []string{"root@team", "fragments@team", "standalone@team", "empty@team", "discovered@team", "unrelated@team", "missing@team"} {
		rowsInput = append(rowsInput, protocol.NewJSONObject(field("member", protocol.NewJSONObject(field("team", "team"), field("agent", id)))))
	}
	rowsInput = append(rowsInput, protocol.NewJSONObject(field("member", protocol.NewJSONObject(field("team", "missing"), field("agent", "x")))), protocol.NewJSONObject(field("member", protocol.NewJSONObject(field("team", "team"), field("agent", "root@team"))), field("options", protocol.NewJSONObject(field("incarnationId", "wrong")))), protocol.NewJSONObject(field("member", protocol.NewJSONObject(field("team", "team"), field("agent", "historical@team"))), field("options", protocol.NewJSONObject(field("leadSessionId", "lead"), field("incarnationId", incarnation(oldTeam))))))
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-transcript-parser.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_TRANSCRIPT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "rows": rowsInput}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Pages        []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Pages) != len(rowsInput) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	for i, row := range rowsInput {
		member := obj(row.Get("member"))
		page, err := reader.GetMemberTranscriptPage(context.Background(), member.String("team"), member.String("agent"), obj(row.Get("options")))
		var failure any
		var data any = page
		if err != nil {
			data = nil
			var api *teamsapi.APIError
			if !errors.As(err, &api) {
				t.Fatal(i, err)
			}
			failure = map[string]any{"status": api.Status, "code": api.Code, "message": api.Message}
		}
		actual := raw(map[string]any{"page": data, "error": failure})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Pages[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, actual, source.Pages[i])
		}
	}
}
