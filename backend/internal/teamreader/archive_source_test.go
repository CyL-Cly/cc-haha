package teamreader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestActualTeamArchiveLegacyMigrationRosterMonotonicityRingAndLookupMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	member := func(id, model, status string) protocol.JSONObject {
		v := protocol.NewJSONObject(field("agentId", id), field("name", id), field("status", status))
		if model != "" {
			v.Set("model", raw(model))
		}
		return v
	}
	snapshot := func(session, team, id, version string, created, at, revision int64, members []protocol.JSONObject) protocol.JSONObject {
		return protocol.NewJSONObject(field("version", version), field("generatedAt", time.UnixMilli(at).UTC().Format("2006-01-02T15:04:05.000Z")), field("taskListRevision", revision), field("team", protocol.NewJSONObject(field("name", team), field("incarnationId", id), field("createdAt", created), field("leadSessionId", session), field("memberCount", len(members)), field("activeMemberCount", len(members)), field("members", members))), field("tasks", []protocol.JSONObject{}), field("messages", []protocol.JSONObject{}))
	}
	first := snapshot("s", "team", "first", "v1", 1000, 2000, 1, []protocol.JSONObject{member("lead", "m", "running"), member("worker", "pinned", "running"), member("failed", "", "failed")})
	second := snapshot("s", "team", "first", "v2", 1000, 3000, 2, []protocol.JSONObject{member("lead", "", "running"), member("new", "new-model", "running")})
	delayed := snapshot("s", "team", "first", "delayed", 1000, 4000, 1, []protocol.JSONObject{})
	equalClock := snapshot("s", "team", "first", "equal-revision-different", 1000, 3000, 2, []protocol.JSONObject{})
	tombstone := second.Clone()
	tombstone.Set("deletedAt", raw(time.UnixMilli(5000).UTC().Format("2006-01-02T15:04:05.000Z")))
	tombstone.Set("generatedAt", tombstone.Get("deletedAt"))
	tombstone.Set("version", raw("v2:deleted"))
	resurrect := snapshot("s", "team", "first", "resurrect", 1000, 6000, 3, []protocol.JSONObject{})
	next := snapshot("s", "team", "second", "new-run", 5500, 7000, 1, []protocol.JSONObject{})
	rowsInput := []protocol.JSONObject{protocol.NewJSONObject(field("session", "s"), field("snapshots", []protocol.JSONObject{first, second, delayed, equalClock, tombstone, resurrect, next}), field("lookups", []protocol.JSONObject{protocol.NewJSONObject(), protocol.NewJSONObject(field("at", 1000)), protocol.NewJSONObject(field("at", 4999)), protocol.NewJSONObject(field("at", 5000)), protocol.NewJSONObject(field("at", 5500)), protocol.NewJSONObject(field("incarnationId", "first")), protocol.NewJSONObject(field("teamName", "missing"))}))}
	legacySnapshot := snapshot("legacy", "old", "", "legacy-v", 9000, 10000, 0, []protocol.JSONObject{member("historic", "old-model", "running")})
	legacyTeam := obj(legacySnapshot.Get("team"))
	legacyTeam.Delete("incarnationId")
	legacyTeam.Set("futureTeam", raw(map[string]bool{"keep": true}))
	legacySnapshot.Set("team", raw(legacyTeam))
	legacySnapshot.Set("futureSnapshot", raw("keep"))
	legacy := protocol.NewJSONObject(field("schemaVersion", 1), field("sessionId", "legacy"), field("futureRoot", map[string]int{"keep": 1}), field("teams", []protocol.JSONObject{protocol.NewJSONObject(field("teamName", "old"), field("updatedAt", "old"), field("futureEntry", "keep"), field("snapshots", []protocol.JSONObject{legacySnapshot, legacySnapshot})), protocol.NewJSONObject(field("teamName", "bad"), field("updatedAt", "t"), field("snapshots", []any{nil, map[string]any{"team": nil}}))}))
	rowsInput = append(rowsInput, protocol.NewJSONObject(field("session", "legacy"), field("initial", string(raw(legacy))), field("lookups", []protocol.JSONObject{protocol.NewJSONObject(), protocol.NewJSONObject(field("at", 9000))})))
	ring := []protocol.JSONObject{}
	for i := 0; i < 205; i++ {
		ring = append(ring, snapshot("ring", "team", "ring-id", time.UnixMilli(int64(i)).String(), 1000, int64(2000+i), int64(i), []protocol.JSONObject{}))
	}
	rowsInput = append(rowsInput, protocol.NewJSONObject(field("session", "ring"), field("snapshots", ring), field("lookups", []protocol.JSONObject{protocol.NewJSONObject()})))
	parallel := []protocol.JSONObject{}
	for i := 0; i < 8; i++ {
		parallel = append(parallel, snapshot("parallel", "team", "parallel-id", string(rune('a'+i)), 1000, int64(2000+i), int64(i), []protocol.JSONObject{}))
	}
	rowsInput = append(rowsInput, protocol.NewJSONObject(field("session", "parallel"), field("parallel", parallel), field("lookups", []protocol.JSONObject{protocol.NewJSONObject()})))
	for i, created := range []json.RawMessage{raw("1000"), raw("0x10"), raw("+20"), raw(true), raw([]int{30}), raw([]int{}), raw("invalid"), json.RawMessage("null")} {
		session := fmt.Sprintf("legacy-number-%d", i)
		snapshot := snapshot(session, "legacy-number", "", "legacy", 1000, 2000, 0, []protocol.JSONObject{})
		team := obj(snapshot.Get("team"))
		team.Delete("incarnationId")
		team.Set("createdAt", created)
		snapshot.Set("team", raw(team))
		doc := protocol.NewJSONObject(field("schemaVersion", 1), field("sessionId", session), field("teams", []protocol.JSONObject{protocol.NewJSONObject(field("teamName", "legacy-number"), field("updatedAt", "valid"), field("snapshots", []protocol.JSONObject{snapshot}))}))
		rowsInput = append(rowsInput, protocol.NewJSONObject(field("session", session), field("initial", string(raw(doc))), field("lookups", []protocol.JSONObject{protocol.NewJSONObject()})))
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-archive.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_ARCHIVE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "rows": rowsInput}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Observations []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Observations) != len(rowsInput) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	configDir := filepath.Join(t.TempDir(), ".claude")
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: configDir, PID: os.Getpid(), Now: time.Now})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: configDir, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: configDir, Tasks: tasks, Mailbox: mailbox, Now: time.Now})
	for i, row := range rowsInput {
		session := row.String("session")
		if row.Get("initial") != nil {
			path := reader.ArchivePath(session)
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			if err = os.WriteFile(path, []byte(row.String("initial")), 0600); err != nil {
				t.Fatal(err)
			}
		}
		var failure error
		for _, snapshot := range rows(row.Get("snapshots")) {
			if failure = reader.ArchiveSnapshot(context.Background(), snapshot); failure != nil {
				break
			}
		}
		for _, snapshot := range rows(row.Get("parallel")) {
			if failure = reader.ArchiveSnapshot(context.Background(), snapshot); failure != nil {
				break
			}
		}
		doc := reader.ReadArchive(session)
		selected := []*protocol.JSONObject{}
		for _, lookup := range rows(row.Get("lookups")) {
			selected = append(selected, reader.SelectArchiveEntry(doc, lookup))
		}
		var physical, errorValue any
		if b, err := os.ReadFile(reader.ArchivePath(session)); err == nil {
			physical = string(b)
		}
		if failure != nil {
			errorValue = failure.Error()
		}
		actual := raw(map[string]any{"document": doc, "selected": selected, "physical": physical, "error": errorValue})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Observations[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("row %d Go %.8000s Source %.8000s", i, actual, source.Observations[i])
		}
	}
}
