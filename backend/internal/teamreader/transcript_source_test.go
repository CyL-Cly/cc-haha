package teamreader

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActualTeamTranscriptPageOrdinalsAppendResetFragmentsNotificationsAndAnchorsMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	first := `{"type":"assistant","uuid":"assistant","timestamp":"2026-10-07T00:00:00.000Z","message":{"content":[{"type":"tool_use","id":"call","name":"TaskUpdate","input":{"taskId":"1","status":"in_progress"}}],"model":"actual-model"}}` + "\n" + `{"type":"system","uuid":"meta","timestamp":"2026-10-07T00:00:00.000Z","isMeta":true,"content":"hidden"}` + "\n" + `bad JSON` + "\n" + `{"type":"user","uuid":"result","timestamp":"2026-10-07T00:00:01.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"call","content":"actual"}]},"toolUseResult":{"backgroundTaskId":"background","future":true}}` + "\n"
	notification := `{"type":"cc-haha-task-notification","uuid":"notice","timestamp":"2026-10-07T00:00:02.000Z","taskNotification":{"taskId":"b","toolUseId":"notice-tool","status":"killed","summary":"first"}}` + "\n" + `{"type":"user","uuid":"xml","timestamp":"2026-10-07T00:00:03.000Z","message":{"role":"user","content":[{"type":"text","text":"<task-notification><tool-use-id>notice-tool</tool-use-id><status>completed</status><summary>Final &lt;summary&gt;</summary></task-notification>"}]}}` + "\n"
	projection := protocol.NewJSONObject(field("ownerAgentId", "fragment"), field("ownerAgentIds", []string{"fragment"}))
	rowsInput := []protocol.JSONObject{protocol.NewJSONObject(field("text", first), field("identity", "file-id"), field("ctime", 1000)), protocol.NewJSONObject(field("text", first+notification), field("identity", "file-id"), field("ctime", 2000), field("previous", 0)), protocol.NewJSONObject(field("text", first+notification), field("identity", "file-id"), field("ctime", 2000), field("previous", 1)), protocol.NewJSONObject(field("text", first+notification), field("identity", "replaced-file"), field("ctime", 2000), field("previous", 1)), protocol.NewJSONObject(field("text", strings.Replace(first, "actual", "changed", 1)+notification), field("identity", "file-id"), field("ctime", 2000), field("previous", 1)), protocol.NewJSONObject(field("text", first+notification), field("options", protocol.NewJSONObject(field("cursor", "bad"), field("signature", "bad"), field("afterOrdinal", 1)))), protocol.NewJSONObject(field("text", first+notification), field("options", protocol.NewJSONObject(field("afterOrdinal", 1)))), protocol.NewJSONObject(field("text", first+notification), field("options", protocol.NewJSONObject(field("afterOrdinal", 1000), field("signature", "not-current")))), protocol.NewJSONObject(field("text", first+notification), field("projection", projection)), protocol.NewJSONObject(field("text", first+notification), field("projection", protocol.NewJSONObject(field("ownerAgentIds", []string{"a", "b"}), field("ownerAgentIdByOrdinal", []string{"a", "", "b", "b", "a"})))), protocol.NewJSONObject(field("text", "")), protocol.NewJSONObject(field("text", `null`+"\n"+`[]`+"\n"+`1`+"\n"+`{"type":"unknown"}`+"\n"+first))}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-transcript-parser.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_TRANSCRIPT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
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
	config := filepath.Join(t.TempDir(), "config")
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: config, Tasks: tasks, Mailbox: mailbox, Now: time.Now})
	pages := []protocol.JSONObject{}
	for i, row := range rowsInput {
		options := obj(row.Get("options"))
		if !options.IsObject() {
			options = protocol.NewJSONObject()
		}
		if index := row.Get("previous"); index != nil {
			n := int(numeric(index))
			for _, key := range []string{"signature", "cursor", "afterOrdinal"} {
				options.Set(key, pages[n].Get(key))
			}
		}
		var identity *string
		if value := row.Get("identity"); value != nil {
			v := row.String("identity")
			identity = &v
		}
		projection := obj(row.Get("projection"))
		var owners, ordinalOwners []string
		_ = json.Unmarshal(projection.Get("ownerAgentIds"), &owners)
		_ = json.Unmarshal(projection.Get("ownerAgentIdByOrdinal"), &ordinalOwners)
		ctime := 0.0
		if row.Get("ctime") != nil {
			ctime = numeric(row.Get("ctime"))
		}
		page := reader.ParseTranscriptBuffer([]byte(row.String("text")), options, ctime, identity, TranscriptProjection{OwnerAgentID: projection.String("ownerAgentId"), OwnerAgentIDs: owners, OwnerByOrdinal: ordinalOwners})
		pages = append(pages, page)
		var a, b any
		_ = json.Unmarshal(raw(page), &a)
		_ = json.Unmarshal(source.Pages[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("row %d Go %s Source %s", i, raw(page), source.Pages[i])
		}
	}
}
