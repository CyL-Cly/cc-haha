package teamreader

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func TestActualIndexedTeamTranscriptUsesSQLiteLocatorsFingerprintAdmissionAndDegradedFallback(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	path := filepath.Join(config, "projects", "p", "indexed.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	text := `{"type":"assistant","uuid":"a","timestamp":"2026-10-07T00:00:00.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"update","name":"TaskUpdate","input":{"taskId":"1","status":"completed"}}]}}` + "\n" + `{"type":"cc-haha-task-notification","uuid":"n","timestamp":"2026-10-07T00:00:01.000Z","taskNotification":{"taskId":"task","toolUseId":"update","status":"completed","summary":"Actual complete"}}` + "\n" + `{"type":"custom-title","customTitle":"title"}` + "\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(filepath.Dir(path), "pending.jsonl")
	if err := os.WriteFile(pending, []byte(text+`{"type":"user","uuid":"tail","message":{"role":"user","content":"partial"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	budget := sessionstore.NewBudget()
	parser := activitystatsapi.NewIndexDateParser(time.UTC)
	index, err := localindex.NewCoordinator(localindex.CoordinatorOptions{Scope: config, Mode: "on", Budget: budget, Reduction: localindex.ReductionOptions{ParseDate: parser.Parse, Compare: localeCompare, Location: time.UTC, Platform: runtime.GOOS}, SourceMetadata: func(path string) (localindex.SourceMetadata, error) {
		info, e := os.Stat(path)
		if e != nil {
			return localindex.SourceMetadata{}, e
		}
		return localindex.SourceMetadata{BirthtimeISO: "1970-01-01T00:00:00.000Z", MtimeISO: info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")}, nil
	}, WatcherOptions: localindex.WatchOptions{Debounce: time.Hour, MaxWait: time.Hour, SafetySweep: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Stop(context.Background()) })
	if err = index.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = index.WaitInitial(context.Background()); err != nil {
		t.Fatal(err)
	}
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: config, Tasks: tasks, Mailbox: mailbox, Now: time.Now, LocalIndex: index, HistoryBudget: budget})
	rowsInput := []protocol.JSONObject{protocol.NewJSONObject(field("indexed", path)), protocol.NewJSONObject(field("indexed", path), field("previous", 0)), protocol.NewJSONObject(field("indexed", path), field("options", protocol.NewJSONObject(field("afterOrdinal", 0)))), protocol.NewJSONObject(field("indexed", path), field("options", protocol.NewJSONObject(field("afterOrdinal", 999), field("signature", "different"), field("cursor", "malformed")))), protocol.NewJSONObject(field("indexed", pending)), protocol.NewJSONObject(field("indexed", path), field("projection", protocol.NewJSONObject(field("ownerAgentId", "fragment"), field("ownerAgentIds", []string{"fragment"})))), protocol.NewJSONObject(field("indexed", path), field("stop", true))}
	pages := []*protocol.JSONObject{}
	for _, row := range rowsInput {
		options := obj(row.Get("options"))
		if !options.IsObject() {
			options = protocol.NewJSONObject()
		}
		if row.Get("previous") != nil {
			previous := pages[int(numeric(row.Get("previous")))]
			if previous == nil {
				t.Fatal("missing real initial indexed page")
			}
			for _, key := range []string{"signature", "cursor", "afterOrdinal"} {
				options.Set(key, previous.Get(key))
			}
		}
		if string(row.Get("stop")) == "true" {
			if err = index.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		p := obj(row.Get("projection"))
		var owners []string
		_ = json.Unmarshal(p.Get("ownerAgentIds"), &owners)
		page := reader.readIndexedTranscript(context.Background(), row.String("indexed"), options, TranscriptProjection{OwnerAgentID: p.String("ownerAgentId"), OwnerAgentIDs: owners})
		pages = append(pages, page)
	}
	// Both implementations build their own real SQLite projection. Remove only
	// the completed private database between producers, preserving the same files.
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(filepath.Join(config, "cc-haha", "db", "index-v1.sqlite") + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-transcript-parser.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_TRANSCRIPT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "rows": rowsInput, "indexed": true}))
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
	for i, page := range pages {
		var a, b any
		actual := raw(page)
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Pages[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, actual, source.Pages[i])
		}
	}
	if pages[0] == nil || pages[0].Get("taskAnchors") != nil || len(rows(pages[0].Get("messages"))) != 1 {
		t.Fatal("real indexed producer did not serve Source's page", pages[0])
	}
}
