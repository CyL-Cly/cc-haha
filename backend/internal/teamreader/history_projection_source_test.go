package teamreader

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func TestActualBoundedTeamEvidencePhysicalIncrementalTailRewriteLRUAndRecordLimitsMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	path := filepath.Join(config, "projects", "p", "lead.jsonl")
	team := `{"type":"assistant","uuid":"create","timestamp":"2026-10-07T00:00:00.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"create","name":"TeamCreate","input":{"team_name":"actual"}},{"type":"text","text":"Model prose omitted"}]}}` + "\n"
	result := `{"type":"user","uuid":"result","timestamp":"2026-10-07T00:00:01.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"create","content":"Created"}]},"toolUseResult":{"team_name":"actual"}}` + "\n"
	prefix := `{"type":"assistant","uuid":"before","message":{"role":"assistant","content":[{"type":"tool_use","id":"before","name":"TaskCreate","input":{}}]}}` + "\n" + `{"type":"user","message":{"role":"user","content":"ordinary prose"}}` + "\n"
	tail := `{"type":"user","uuid":"worker","message":{"role":"user","content":"<teammate-message teammate_id=\"worker\">Actual reply</teammate-message>"}}`
	rowsInput := []protocol.JSONObject{protocol.NewJSONObject(field("path", path), field("write", prefix+team), field("mtime", 1000)), protocol.NewJSONObject(field("path", path)), protocol.NewJSONObject(field("path", path), field("append", result), field("mtime", 1001)), protocol.NewJSONObject(field("path", path), field("append", tail), field("mtime", 1002)), protocol.NewJSONObject(field("path", path), field("append", "\n"), field("mtime", 1003)), protocol.NewJSONObject(field("path", path), field("write", team+result), field("mtime", 1004)), protocol.NewJSONObject(field("path", path), field("append", "bad JSON\n"), field("mtime", 1005)), protocol.NewJSONObject(field("path", path), field("write", team+strings.Repeat("x", 1024*1024+1)+"\n"+result), field("mtime", 1006))}
	for i := 0; i < 5; i++ {
		rowsInput = append(rowsInput, protocol.NewJSONObject(field("path", filepath.Join(home, "cache-"+string(rune('a'+i))+".jsonl")), field("write", team), field("mtime", 2000+i)))
	}
	rowsInput = append(rowsInput, protocol.NewJSONObject(field("path", path)))
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: config, Tasks: tasks, Mailbox: mailbox, Now: time.Now, HistoryBudget: sessionstore.NewBudget()})
	observations := []protocol.JSONObject{}
	for _, row := range rowsInput {
		p := row.String("path")
		if row.Get("write") != nil {
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(row.String("write")), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if row.Get("append") != nil {
			file, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = file.WriteString(row.String("append"))
			_ = file.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		if row.Get("mtime") != nil {
			mtime := time.Unix(int64(numeric(row.Get("mtime"))), 0)
			if err := os.Chtimes(p, mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
		value, err := reader.ReadTeamProjection(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, protocol.NewJSONObject(field("messages", value.Messages), field("complete", value.Complete), field("sourceVersion", value.SourceVersion), field("readBytes", value.ReadBytes)))
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-history-projection.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_PROJECTION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
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
	if err = json.Unmarshal(output, &source); err != nil || len(source.Observations) != len(observations) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	for i, observation := range observations {
		actual := raw(observation)
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Observations[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, actual, source.Observations[i])
		}
	}
}
