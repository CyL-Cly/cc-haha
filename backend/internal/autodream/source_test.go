package autodream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type lockCase struct {
	Body   *string `json:"body"`
	AgeMS  int64   `json:"ageMs"`
	Finish string  `json:"finish"`
}
type lockObservation struct {
	BeforeAge                      *float64
	PriorAge                       *float64
	Acquired, BodyHasPrior, Exists bool
	CompletedBody                  *string
	StampPreserved                 *bool
}

func TestActualSourceDreamLocksPromptsCandidatesAndTasks(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	allocation, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(allocation, "source")
	if err = os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	text := func(v string) *string { return &v }
	hour := int64(time.Hour / time.Millisecond)
	rows := []lockCase{{nil, 0, "rollback"}, {text(""), 48 * hour, "complete"}, {text("$SELF"), 0, "complete"}, {text("$SELF"), 2 * hour, "complete"}, {text("2147483647"), 48 * hour, "rollback"}, {text("2147483647 auto-dream-v1 12345.5"), 0, "rollback"}, {text("$SELF auto-dream-v1 7777"), 0, "complete"}, {text("$SELF auto-dream-v1 7777"), 2 * hour, "complete"}, {text("invalid"), 48 * hour, "complete"}, {text("1 auto-dream-v1 8888"), 48 * hour, "complete"}, {text("\ufeff$SELF legacy"), 0, "complete"}}
	files := []map[string]any{{"name": "11111111-1111-1111-1111-111111111111.jsonl", "mtime": 2000}, {"name": "ABCDEFAB-1234-5678-ABCD-123456789abc.jsonl", "mtime": 2001}, {"name": "22222222-2222-2222-2222-222222222222.jsonl", "mtime": 1000}, {"name": "33333333-3333-3333-3333-333333333333.jsonl", "mtime": 2001, "directory": true}, {"name": "agent-abc.jsonl", "mtime": 3000}, {"name": "not-uuid.jsonl", "mtime": 3000}, {"name": "44444444-4444-4444-4444-444444444444.JSONL", "mtime": 3000}}
	prompts := []map[string]string{{"memory": "/private/tmp/memory/", "transcripts": "/private/tmp/transcripts", "extra": ""}, {"memory": "/private/tmp/中文 space/", "transcripts": "/private/tmp/历史", "extra": "fixture signal\n- session"}}
	actions := []map[string]any{{"type": "turn", "turn": map[string]any{"text": "", "toolUseCount": 0}, "paths": []string{}}, {"type": "turn", "turn": map[string]any{"text": "reviewing", "toolUseCount": 2}, "paths": []string{"/m/a.md", "/m/a.md"}}, {"type": "turn", "turn": map[string]any{"text": "", "toolUseCount": 0}, "paths": []string{"/m/a.md"}}}
	for i := 0; i < 34; i++ {
		actions = append(actions, map[string]any{"type": "turn", "turn": map[string]any{"text": "turn", "toolUseCount": i % 2}, "paths": []string{}})
	}
	actions = append(actions, map[string]any{"type": "complete"}, map[string]any{"type": "kill"})
	tasks := []map[string]any{{"actions": actions}, {"actions": []map[string]any{{"type": "fail"}, {"type": "kill"}}}, {"actions": []map[string]any{{"type": "kill"}, {"type": "kill"}}}}
	configs := []map[string]any{}
	for _, raw := range []any{nil, "invalid", []any{}, map[string]any{}, map[string]any{"enabled": true}, map[string]any{"enabled": false, "minHours": 0, "minSessions": -1}, map[string]any{"enabled": true, "minHours": 0.125, "minSessions": 1.5}, map[string]any{"enabled": "true", "minHours": "2", "minSessions": true}, map[string]any{"enabled": true, "minHours": 1e100, "minSessions": 4.75}} {
		for _, settings := range []map[string]any{{}, {"autoDreamEnabled": true}, {"autoDreamEnabled": false}, {"autoDreamEnabled": true, "autoMemoryEnabled": false}} {
			configs = append(configs, map[string]any{"raw": raw, "settings": settings})
		}
	}
	configs = append(configs, map[string]any{"raw": map[string]any{"enabled": true}, "kairos": true}, map[string]any{"raw": map[string]any{"enabled": true}, "remote": true})
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "locks": rows, "files": files, "since": 1000, "prompts": prompts, "tasks": tasks, "configs": configs})
	fixture, _ := filepath.Abs("../../testdata/native-autodream.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Stdin = bytes.NewReader(payload)
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(home, "tmp"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_AUTODREAM_SOURCE_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Now                           float64
		LockResults                   []lockObservation
		Sessions, Candidates, Prompts []string
		Transcript                    string
		Tasks                         []struct {
			Snapshots []protocol.JSONObject
			Aborted   bool
			Events    []struct{ Status, Summary, OutputFile string }
		}
		Blocked []string
		Configs []struct {
			Config Config
			Gate   bool
		}
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Blocked) > 0 || len(source.LockResults) != len(rows) {
		t.Fatal(err, string(out))
	}
	for i, row := range rows {
		root := filepath.Join(allocation, "go", string(rune('a'+i)))
		if err = os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		s, err := New(Options{MemoryDirectory: func() (string, error) { return root, nil }, TranscriptDirectory: func() (string, error) { return root, nil }, NowMillis: func() float64 { return source.Now }, PID: os.Getpid(), IsProcessRunning: func(pid int) bool { return pid == os.Getpid() }})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, ".consolidate-lock")
		if row.Body != nil {
			body := strings.ReplaceAll(*row.Body, "$SELF", strconvPID())
			if err = os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			at := time.UnixMilli(int64(source.Now) - row.AgeMS)
			if err = os.Chtimes(path, at, at); err != nil {
				t.Fatal(err)
			}
		}
		before := s.ReadLastConsolidatedAt()
		prior, err := s.TryAcquire()
		if err != nil {
			t.Fatal(err)
		}
		got := lockObservation{Acquired: prior != nil}
		if before != 0 {
			age := source.Now - before
			got.BeforeAge = &age
		}
		if prior != nil {
			age := *prior
			if age != 0 {
				age = source.Now - age
			}
			got.PriorAge = &age
			body, _ := os.ReadFile(path)
			got.BodyHasPrior = strings.Contains(string(body), " auto-dream-v1 ")
			info, _ := os.Stat(path)
			mtime := info.ModTime()
			if row.Finish == "complete" {
				s.Complete()
				body, _ = os.ReadFile(path)
				str := string(body)
				got.CompletedBody = &str
				next, _ := os.Stat(path)
				same := next.ModTime().Equal(mtime)
				got.StampPreserved = &same
			}
			if row.Finish == "rollback" {
				s.Rollback(*prior)
			}
		}
		_, err = os.Stat(path)
		got.Exists = err == nil
		if !reflect.DeepEqual(got, source.LockResults[i]) {
			t.Fatalf("lock %d: Go %#v Source %#v", i, got, source.LockResults[i])
		}
	}
	for i, row := range prompts {
		if got := BuildConsolidationPrompt(row["memory"], row["transcripts"], row["extra"]); got != source.Prompts[i] {
			t.Fatalf("prompt %d differs\n%s", i, got)
		}
	}
	// Both implementations observe the same physical directory: APFS native
	// enumeration order is not guaranteed to agree across two different dirs.
	transcript := source.Transcript
	if !strings.HasPrefix(transcript, home+string(filepath.Separator)) {
		t.Fatal("Source transcript escaped disposable home")
	}
	s, _ := New(Options{MemoryDirectory: func() (string, error) { return transcript, nil }, TranscriptDirectory: func() (string, error) { return transcript, nil }, PID: os.Getpid(), IsProcessRunning: func(int) bool { return false }})
	ids, err := s.ListSessionsTouchedSince(1000)
	if err != nil || !reflect.DeepEqual(ids, source.Sessions) {
		t.Fatal(ids, source.Sessions, err)
	}
	for i, row := range tasks {
		src := source.Tasks[i]
		first := src.Snapshots[0]
		start, _ := taskNumber(first, "startTime")
		task := RegisterTask(first.String("id"), first.String("outputFile"), 5, 0, start)
		compare := func(j int) {
			t.Helper()
			expected := src.Snapshots[j].Clone()
			expected.Delete("abortController")
			a, _ := task.MarshalJSON()
			b, _ := expected.MarshalJSON()
			var aa, bb any
			_ = json.Unmarshal(a, &aa)
			_ = json.Unmarshal(b, &bb)
			if !reflect.DeepEqual(aa, bb) {
				t.Fatalf("task %d step %d: Go %s Source %s", i, j, a, b)
			}
		}
		compare(0)
		events := []struct{ Status, Summary, OutputFile string }{}
		for j, action := range row["actions"].([]map[string]any) {
			switch action["type"] {
			case "turn":
				raw, _ := json.Marshal(action["turn"])
				var turn protocol.JSONObject
				_ = json.Unmarshal(raw, &turn)
				raw, _ = json.Marshal(action["paths"])
				var paths []json.RawMessage
				_ = json.Unmarshal(raw, &paths)
				task, _ = AddTurn(task, turn, paths)
			case "complete", "fail":
				now, _ := taskNumber(src.Snapshots[j+1], "endTime")
				status := "completed"
				if action["type"] == "fail" {
					status = "failed"
				}
				var event *TerminalEvent
				task, event = FinishTask(task, status, now)
				if event != nil {
					events = append(events, struct{ Status, Summary, OutputFile string }{event.Status, event.Summary, ""})
				}
			case "kill":
				now, _ := taskNumber(src.Snapshots[j+1], "endTime")
				var event *TerminalEvent
				task, event, _ = KillTask(task, now)
				if event != nil {
					events = append(events, struct{ Status, Summary, OutputFile string }{event.Status, event.Summary, ""})
				}
			}
			compare(j + 1)
		}
		if !reflect.DeepEqual(events, src.Events) {
			t.Fatal(i, events, src.Events)
		}
	}
	if len(source.Configs) != len(configs) {
		t.Fatal("actual Source config corpus missing", len(source.Configs))
	}
	for i, row := range configs {
		raw, _ := json.Marshal(row["raw"])
		var feature protocol.JSONObject
		_ = json.Unmarshal(raw, &feature)
		if got := Configuration(feature); got != source.Configs[i].Config {
			t.Fatal(i, got, source.Configs[i].Config)
		}
		settings, _ := row["settings"].(map[string]any)
		var setting *bool
		if v, ok := settings["autoDreamEnabled"].(bool); ok {
			setting = &v
		}
		memory := settings["autoMemoryEnabled"] != false
		gate := row["kairos"] != true && row["remote"] != true && memory && Enabled(setting, feature)
		if gate != source.Configs[i].Gate {
			t.Fatal("actual gate", i, gate, source.Configs[i].Gate)
		}
	}
}
func strconvPID() string { return fmt.Sprint(os.Getpid()) }

func taskNumber(o protocol.JSONObject, key string) (float64, error) {
	var n float64
	err := json.Unmarshal(o.Get(key), &n)
	return n, err
}
