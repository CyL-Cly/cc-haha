package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourceClearLocalCallerTaskPartitionAndPreservedSkills(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Pinned actual Source required")
	}
	tasks := []map[string]any{{"id": "fg", "type": "local_bash", "status": "running", "isBackgrounded": false}, {"id": "kept", "type": "local_agent", "status": "completed", "agentId": "kept-agent", "isBackgrounded": true}, {"id": "absent", "type": "local_bash", "status": "completed"}, {"id": "null", "type": "local_bash", "status": "completed", "isBackgrounded": nil}}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), []byte("{}"), 0600)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-clear.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	sid := "7f6cd65c-af93-4c2a-80a1-91fffc4f81c4"
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLEAR_FIXTURE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "sid": sid, "tasks": tasks, "transcript": "OLD PRIVATE TRANSCRIPT\n"}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Registration struct {
			Name, Kind     string
			Aliases        []string
			NonInteractive bool
		}
		Sid, Parent, ConversationID, OldTranscript string
		Messages                                   []protocol.JSONObject
		Tasks, Aborted, Skills                     []string
		FileHistory                                struct{ Snapshots, Tracked, Sequence int }
		MCP                                        struct{ Clients, Tools, Commands, Resources, Key int }
		ReadFiles, Discovered, Nested              int
		LastDate                                   json.RawMessage
		Standalone, Title, Color                   json.RawMessage
		Caps                                       struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if source.Registration.Name != "clear" || source.Registration.Kind != "local" || source.Registration.NonInteractive || !reflect.DeepEqual(source.Registration.Aliases, []string{"reset", "new"}) || source.Sid == sid || source.Parent != sid || source.ConversationID == "" || len(source.Messages) != 0 || source.OldTranscript != "OLD PRIVATE TRANSCRIPT\n" || source.MCP.Key != 19 || source.MCP.Clients+source.MCP.Tools+source.MCP.Commands+source.MCP.Resources != 0 || source.FileHistory.Snapshots+source.FileHistory.Tracked+source.FileHistory.Sequence != 0 || source.ReadFiles+source.Discovered+source.Nested != 0 || string(source.LastDate) != "null" || source.Standalone != nil || source.Title != nil || source.Color != nil || len(source.Caps.Blocked)+len(source.Caps.SecureReads) != 0 {
		t.Fatal(string(output))
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:9")
	o.Interactive = true
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var foreground context.Context
	if err = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, data := range tasks {
			worker, cancel := context.WithCancelCause(context.Background())
			if data["id"] == "fg" {
				foreground = worker
			}
			entry := &sessioncontroller.BackgroundTask{Data: obj(raw(data)), Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return raw(""), nil }, Join: func(context.Context) error { return nil }}
			if err := s.process.Controller.RegisterBackgroundTaskOnOwner(scope, entry); err != nil {
				return err
			}
		}
		for _, agent := range []string{"", "kept-agent", "dropped-agent"} {
			if err := s.process.Controller.InvokeSkill(scope, "owned-skill", filepath.Join(o.WorkDir, "skill"), "owned full skill text", agent); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "/clear", Mode: "prompt"}, func(tui.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if foreground.Err() == nil {
		t.Fatal("Actual foreground cancellation omitted")
	}
	var actualTasks []string
	if err = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		rows, e := s.process.Controller.BackgroundTasksOnOwner(scope)
		if e != nil {
			return e
		}
		for _, row := range rows {
			actualTasks = append(actualTasks, row.String("id"))
		}
		for _, agent := range []string{"", "kept-agent", "dropped-agent"} {
			skills, e := s.process.Controller.CompactInvokedSkillsOnOwner(scope, agent)
			if e != nil {
				return e
			}
			want := 0
			if agent == "kept-agent" {
				want = 1
			}
			if len(skills) != want {
				t.Fatal("Per-agent skill state changed", agent, skills)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(actualTasks)
	sort.Strings(source.Tasks)
	if !reflect.DeepEqual(actualTasks, source.Tasks) || !reflect.DeepEqual(source.Aborted, []string{"fg"}) || !reflect.DeepEqual(source.Skills, []string{"kept-agent:owned-skill"}) {
		t.Fatal(actualTasks, string(output))
	}
}
