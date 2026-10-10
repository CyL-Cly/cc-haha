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

func TestActualPhysicalTranscriptFragmentsPrefixReplacerBudgetsAndNativeSnapshotMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	message := `{"type":"assistant","uuid":"same","timestamp":"2026-10-07T00:00:00.000Z","message":{"content":"old"},"cwd":"old","agentId":"old"}` + "\n"
	// The original JSON.stringify replacer filters nested content with the
	// root key list. Changing this nested text therefore remains a prefix.
	rewritten := bytes.ReplaceAll([]byte(message), []byte(`"old"`), []byte(`"rewritten"`))
	next := `{"type":"user","uuid":"next","timestamp":"2026-10-07T00:00:01.000Z","message":{"content":"new"}}` + "\n"
	contents := []string{message, string(rewritten) + next, message, "null\nfalse\n[]\n[{\"nested\":1}]\n" + message, "bad JSON\n\n" + next, ""}
	paths := []TranscriptFile{}
	for i, content := range contents {
		path := filepath.Join(home, "fragment-"+string(rune('a'+i))+".jsonl")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, TranscriptFile{path, string(rune('a' + i))})
	}
	large := filepath.Join(home, "large.jsonl")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(8*1024*1024 + 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	many := make([]TranscriptFile, 129)
	for i := range many {
		many[i] = paths[0]
	}
	cases := [][]TranscriptFile{{paths[0]}, {paths[0], paths[1]}, {paths[0], paths[2]}, {paths[3], paths[4]}, {paths[5], paths[0]}, {}, {{large, ""}}, many}
	inputRows := []protocol.JSONObject{}
	for _, sources := range cases {
		files := []protocol.JSONObject{}
		for _, source := range sources {
			files = append(files, protocol.NewJSONObject(field("filePath", source.Path), field("ownerAgentId", source.OwnerAgentID)))
		}
		inputRows = append(inputRows, protocol.NewJSONObject(field("files", files)))
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-transcript-parser.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_TRANSCRIPT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "rows": inputRows}))
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
	if err = json.Unmarshal(output, &source); err != nil || len(source.Pages) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	config := filepath.Join(home, ".claude")
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: config, Tasks: tasks, Mailbox: mailbox, Now: time.Now})
	for i, paths := range cases {
		page, err := reader.ParseTranscriptFiles(context.Background(), paths, protocol.NewJSONObject())
		var failure any
		var actualPage any = page
		if err != nil {
			actualPage = nil
			var api *teamsapi.APIError
			if !errors.As(err, &api) {
				t.Fatal(i, err)
			}
			failure = map[string]any{"status": api.Status, "code": api.Code, "message": api.Message}
		}
		actual := raw(map[string]any{"page": actualPage, "error": failure})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Pages[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, actual, source.Pages[i])
		}
	}
}
