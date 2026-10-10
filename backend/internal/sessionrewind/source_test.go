package sessionrewind

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeFixtureFile struct {
	Path string `json:"path"`
	Hex  string `json:"hex"`
}
type diffRequest struct {
	Selector Selector `json:"selector"`
	Path     string   `json:"path"`
}
type rewindFixture struct {
	Home               string                `json:"home"`
	Messages           []protocol.JSONObject `json:"messages"`
	Rows               []json.RawMessage     `json:"rows"`
	Complete           bool                  `json:"complete"`
	Frozen             bool                  `json:"frozen"`
	Files              []nativeFixtureFile   `json:"files"`
	Commands           []string              `json:"commands"`
	ClassifierPlatform string                `json:"classifierPlatform,omitempty"`
	ClassifierUserType string                `json:"classifierUserType,omitempty"`
	DiffRequests       []diffRequest         `json:"diffRequests,omitempty"`
	PreviewRequests    []Selector            `json:"previewRequests,omitempty"`
	ExecuteRequest     *executeRequest       `json:"executeRequest,omitempty"`
	ObservePaths       []string              `json:"observePaths,omitempty"`
	Modes              map[string]uint32     `json:"modes,omitempty"`
	TrimError          string                `json:"trimError,omitempty"`
	LateMessages       []protocol.JSONObject `json:"lateMessages,omitempty"`
}

func quoted(value string) json.RawMessage     { raw, _ := json.Marshal(value); return raw }
func record(value string) protocol.JSONObject { return object(json.RawMessage(value)) }
func fixtureMessage(id, kind, cwd string, content any, parent string) protocol.JSONObject {
	raw, _ := json.Marshal(map[string]any{"id": id, "type": kind, "cwd": cwd, "content": content, "parentToolUseId": parent})
	return object(raw)
}
func fixtureTool(id, name string, input any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}
func fixtureResult(id string, failed bool) map[string]any {
	return map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": failed}
}
func fixtureRow(id string, before, completed any, completion bool) json.RawMessage {
	snap := map[string]any{"messageId": id, "trackedFileBackups": before}
	if completion {
		snap["completedFileBackups"] = completed
	}
	raw, _ := json.Marshal(map[string]any{"type": "file-history-snapshot", "snapshot": snap})
	return raw
}
func fixtureBackup(name any) map[string]any {
	return map[string]any{"backupFileName": name, "version": 1}
}
func fixtureService(t *testing.T, input rewindFixture) *Service {
	t.Helper()
	files := NativeFiles{ConfigHome: filepath.Join(input.Home, ".claude")}
	service, err := New(Options{
		MessagesWithEvidence: func(context.Context, string) (Evidence, error) { return Evidence{input.Messages, input.Complete}, nil },
		Snapshots: func(context.Context, string) (SnapshotEvidence, error) {
			return SnapshotEvidence{input.Rows, true}, nil
		}, WorkDir: func(context.Context, string) (string, error) { return filepath.Join(input.Home, "project"), nil },
		ProcessCWD: files.CanonicalPath(input.Home), Files: files, ReadBackup: files.ReadBackup, ReadCurrent: files.ReadCurrent, SafeTrackedPath: files.SafeTrackedPath, CanonicalPath: files.CanonicalPath, RecordedCommandIsReadOnly: func(string) bool { return false }, Platform: runtime.GOOS,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func TestActualSourceTurnCheckpoints(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source evidence requires pinned Bun 1.3.14")
	}
	source, err := filepath.Abs("testdata/source.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"frozen-complete", "live-later-bytes", "child-after-pending-turn", "failed-write-covered", "uncertain-write-uncovered", "invalid-utf8-live", "invalid-utf8-frozen", "malformed-completion", "legacy-next-alias-conflict", "carried-before-state", "empty-file-versus-absent", "incomplete-transcript", "numeric-and-unicode-order", "malformed-before-map", "empty-recorded-user-cwd", "empty-recorded-tool-cwd"} {
		t.Run(name, func(t *testing.T) {
			allocation := t.TempDir()
			home := filepath.Join(allocation, "home")
			for _, leaf := range []string{"", "tmp", "config", "cache"} {
				if err := os.MkdirAll(filepath.Join(home, leaf), 0700); err != nil {
					t.Fatal(err)
				}
			}
			project := filepath.Join(home, "project")
			backupDir := filepath.Join(home, ".claude", "file-history", "fixture-session")
			input := rewindFixture{Home: home, Complete: true, Frozen: true, Rows: []json.RawMessage{}, Files: []nativeFixtureFile{}, Commands: []string{}}
			file := func(path, value string) {
				input.Files = append(input.Files, nativeFixtureFile{path, hex.EncodeToString([]byte(value))})
			}
			input.Messages = []protocol.JSONObject{fixtureMessage("u1", "user", project, "one", ""), fixtureMessage("a1", "assistant", project, "done", "")}
			before := map[string]any{"a.txt": fixtureBackup("before")}
			after := map[string]any{"a.txt": fixtureBackup("after")}
			input.Rows = []json.RawMessage{fixtureRow("u1", before, after, true)}
			file(filepath.Join(backupDir, "before"), "old\n")
			file(filepath.Join(backupDir, "after"), "new\n")
			file(filepath.Join(project, "a.txt"), "later\nextra\n")
			switch name {
			case "live-later-bytes":
				input.Frozen = false
			case "child-after-pending-turn":
				input.Rows = []json.RawMessage{}
				input.Messages = []protocol.JSONObject{fixtureMessage("u1", "user", project, "one", ""), fixtureMessage("agent-call", "tool_use", project, []any{fixtureTool("agent", "Agent", map[string]any{})}, ""), fixtureMessage("u2", "user", project, "pending", ""), fixtureMessage("child", "tool_use", project, []any{fixtureTool("write", "Write", map[string]any{"file_path": "child.txt", "content": "child 中文\n"})}, "agent"), fixtureMessage("result", "tool_result", project, []any{fixtureResult("write", false)}, "agent")}
			case "failed-write-covered":
				input.Messages = append(input.Messages, fixtureMessage("write", "tool_use", project, []any{fixtureTool("write", "Write", map[string]any{"file_path": "a.txt", "content": "new\n"})}, ""), fixtureMessage("success", "tool_result", project, []any{fixtureResult("write", false)}, ""), fixtureMessage("failure", "tool_result", project, []any{fixtureResult("write", true)}, ""))
			case "uncertain-write-uncovered":
				input.Rows = []json.RawMessage{}
				input.Messages = append(input.Messages, fixtureMessage("write", "tool_use", project, []any{fixtureTool("write", "Write", map[string]any{"file_path": "a.txt", "content": "new\n"})}, ""))
			case "invalid-utf8-live", "invalid-utf8-frozen":
				input.Frozen = name == "invalid-utf8-frozen"
				input.Files = nil
				file(filepath.Join(backupDir, "before"), string([]byte{255}))
				file(filepath.Join(backupDir, "after"), string([]byte{254}))
				file(filepath.Join(project, "a.txt"), string([]byte{254}))
			case "malformed-completion":
				input.Rows = []json.RawMessage{fixtureRow("u1", before, nil, true), fixtureRow("u2", after, nil, false)}
				input.Messages = append(input.Messages, fixtureMessage("u2", "user", project, "pending", ""))
			case "legacy-next-alias-conflict":
				input.Rows = []json.RawMessage{fixtureRow("u1", before, nil, false), fixtureRow("u2", map[string]any{"a.txt": fixtureBackup("after"), "./a.txt": fixtureBackup("before")}, nil, false)}
				input.Messages = append(input.Messages, fixtureMessage("u2", "user", project, "pending", ""))
			case "carried-before-state":
				input.Rows = append(input.Rows, fixtureRow("u2", before, after, true))
				input.Messages = append(input.Messages, fixtureMessage("write", "tool_use", project, []any{fixtureTool("write", "Write", map[string]any{"file_path": "a.txt", "content": "new\n"})}, ""), fixtureMessage("result", "tool_result", project, []any{fixtureResult("write", false)}, ""), fixtureMessage("u2", "user", project, "second", ""), fixtureMessage("a2", "assistant", project, "done", ""))
			case "empty-file-versus-absent":
				input.Rows = []json.RawMessage{fixtureRow("u1", map[string]any{"empty": fixtureBackup(nil)}, map[string]any{"empty": fixtureBackup("empty")}, true)}
				file(filepath.Join(backupDir, "empty"), "")
			case "incomplete-transcript":
				input.Complete = false
			case "numeric-and-unicode-order":
				input.Rows = []json.RawMessage{}
				input.Messages = []protocol.JSONObject{fixtureMessage("u1", "user", project, "one", "")}
				for i, path := range []string{"z.txt", "中文.txt", "é.txt", "e.txt", "😀.txt", "2.txt", "10.txt"} {
					id := string(rune('a' + i))
					input.Messages = append(input.Messages, fixtureMessage(id, "tool_use", project, []any{fixtureTool(id, "Write", map[string]any{"file_path": path, "content": "x\n"})}, ""), fixtureMessage(id+"result", "tool_result", project, []any{fixtureResult(id, false)}, ""))
				}
			case "empty-recorded-user-cwd":
				input.Messages[0] = fixtureMessage("u1", "user", "", "one", "")
				file(filepath.Join(home, "a.txt"), "later\n")
			case "empty-recorded-tool-cwd":
				input.Messages = append(input.Messages, fixtureMessage("write", "tool_use", "", []any{fixtureTool("write", "Write", map[string]any{"file_path": "a.txt", "content": "new\n"})}, ""), fixtureMessage("result", "tool_result", project, []any{fixtureResult("write", false)}, ""))
				file(filepath.Join(home, "a.txt"), "new\n")
			case "malformed-before-map":
				input.Rows = []json.RawMessage{fixtureRow("u1", []any{}, map[string]any{"a.txt": fixtureBackup("after")}, true)}
			}
			zero, mismatch := 0, 1
			input.PreviewRequests = []Selector{{TargetUserMessageID: "u1"}, {UserMessageIndex: &zero}, {TargetUserMessageID: "missing", UserMessageIndex: &zero}, {TargetUserMessageID: "u1", ExpectedContent: stringPointer("wrong prompt")}}
			input.DiffRequests = []diffRequest{{Selector{TargetUserMessageID: "u1"}, "a.txt"}, {Selector{UserMessageIndex: &zero}, filepath.Join(project, "a.txt")}, {Selector{TargetUserMessageID: "u1"}, "./a.txt"}, {Selector{TargetUserMessageID: "u1"}, "missing"}, {Selector{TargetUserMessageID: "u1"}, "child.txt"}, {Selector{TargetUserMessageID: "missing", UserMessageIndex: &zero}, "a.txt"}, {Selector{TargetUserMessageID: "u1", UserMessageIndex: &mismatch}, "a.txt"}, {Selector{TargetUserMessageID: "a1"}, "a.txt"}, {Selector{TargetUserMessageID: "u1", ExpectedContent: stringPointer("wrong prompt")}, "a.txt"}}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", source)
			cmd.Dir = home
			cmd.Stdin = bytes.NewReader(raw)
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "CC_HAHA_REWIND_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "PATH=/usr/bin:/bin", "SHELL=/bin/zsh", "USER_TYPE=external", "ANTHROPIC_API_KEY=fixture", "TZ=UTC", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("Source failed %v\n%s", err, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("Source diagnostic: %s", stderr.String())
			}
			var expected struct {
				Checkpoints  []TurnCheckpoint                     `json:"checkpoints"`
				Diffs        []json.RawMessage                    `json:"diffs"`
				Previews     []json.RawMessage                    `json:"previews"`
				Capabilities struct{ Blocked, SecureReads []any } `json:"capabilities"`
			}
			if err = json.Unmarshal(output, &expected); err != nil {
				t.Fatalf("Source output %s: %v", output, err)
			}
			if len(expected.Capabilities.Blocked) != 0 || len(expected.Capabilities.SecureReads) != 0 {
				t.Fatalf("Source capability violation %s", output)
			}
			service := fixtureService(t, input)
			actual, err := service.List(context.Background(), "fixture-session", input.Frozen)
			if err != nil {
				t.Fatal(err)
			}
			for i, selector := range input.PreviewRequests {
				value, err := service.Preview(context.Background(), "fixture-session", selector)
				var raw []byte
				if err != nil {
					raw, _ = json.Marshal(map[string]string{"error": err.Error()})
				} else {
					raw, _ = json.Marshal(value)
				}
				var got, want any
				if json.Unmarshal(raw, &got) != nil || json.Unmarshal(expected.Previews[i], &want) != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("preview %d Go %s; Source %s", i, raw, expected.Previews[i])
				}
			}
			for i, request := range input.DiffRequests {
				value, err := service.CheckpointDiff(context.Background(), "fixture-session", request.Selector, request.Path, input.Frozen)
				var raw []byte
				if err != nil {
					raw, _ = json.Marshal(map[string]string{"error": err.Error()})
				} else {
					raw, _ = json.Marshal(value)
				}
				var got, want any
				if json.Unmarshal(raw, &got) != nil || json.Unmarshal(expected.Diffs[i], &want) != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("diff %d %q Go %s; Source %s", i, request.Path, raw, expected.Diffs[i])
				}
			}
			actualJSON, _ := json.Marshal(actual)
			expectedJSON, _ := json.Marshal(expected.Checkpoints)
			var actualPublic, expectedPublic any
			_ = json.Unmarshal(actualJSON, &actualPublic)
			_ = json.Unmarshal(expectedJSON, &expectedPublic)
			if !reflect.DeepEqual(actualPublic, expectedPublic) {
				a, _ := json.Marshal(actual)
				t.Fatalf("Go %s\nSource %s", a, output)
			}
		})
	}
}

func stringPointer(value string) *string { return &value }
