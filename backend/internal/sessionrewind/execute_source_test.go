package sessionrewind

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type executeRequest struct {
	Selector Selector        `json:"selector"`
	Mode     json.RawMessage `json:"mode,omitempty"`
}

func runSourceRewindFixture(t *testing.T, input rewindFixture) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source evidence requires pinned Bun 1.3.14")
	}
	source, err := filepath.Abs("testdata/source.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", source)
	cmd.Dir = input.Home
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Env = []string{"HOME=" + input.Home, "USERPROFILE=" + input.Home, "CLAUDE_CONFIG_DIR=" + filepath.Join(input.Home, ".claude"), "TMPDIR=" + filepath.Join(input.Home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(input.Home, "config"), "XDG_CACHE_HOME=" + filepath.Join(input.Home, "cache"), "CC_HAHA_REWIND_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(input.Home), "PATH=/usr/bin:/bin", "SHELL=/bin/zsh", "USER_TYPE=external", "ANTHROPIC_API_KEY=fixture", "TZ=UTC", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("actual Source: %v\n%s", err, stderr.String())
	}
	return output
}

func comparePublic(t *testing.T, name string, value any, raw json.RawMessage) {
	t.Helper()
	got, _ := json.Marshal(value)
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal(raw, &b) != nil || !reflect.DeepEqual(a, b) {
		t.Errorf("%s Go %s\nSource %s", name, got, raw)
	}
}

type observedFile struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Hex    string `json:"hex,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
}

func observedNative(t *testing.T, paths []string) []observedFile {
	t.Helper()
	result := []observedFile{}
	for _, path := range paths {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			result = append(result, observedFile{Path: path})
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		mode := uint32(info.Mode().Perm())
		if info.Mode()&os.ModeSetuid != 0 {
			mode |= 04000
		}
		if info.Mode()&os.ModeSetgid != 0 {
			mode |= 02000
		}
		if info.Mode()&os.ModeSticky != 0 {
			mode |= 01000
		}
		result = append(result, observedFile{path, true, hex.EncodeToString(content), mode})
	}
	return result
}

func TestActualSourceRewindExecution(t *testing.T) {
	if os.Getenv("CC_HAHA_PINNED_BUN") == "" {
		t.Skip("actual Source evidence requires pinned Bun 1.3.14")
	}
	for _, name := range []string{"both-byte-and-mode", "conversation-only", "binary-backup", "create-and-delete", "multi-turn-net-noop", "multi-turn-union", "missing-before", "uncertain-uncovered", "incomplete-transcript", "trim-failure-rolls-back", "stop-drains-late-mutation", "stop-rechecks-prompt", "invalid-mode", "latest-record-keeps-order", "legacy-no-checkpoints"} {
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
			path := filepath.Join(project, "a.txt")
			input := rewindFixture{Home: home, Complete: true, Frozen: true, Commands: []string{}, Messages: []protocol.JSONObject{fixtureMessage("u1", "user", project, "one", ""), fixtureMessage("a1", "assistant", project, "done", "")}, ObservePaths: []string{path}, Modes: map[string]uint32{path: 0600, filepath.Join(backupDir, "before"): 0640}, ExecuteRequest: &executeRequest{Selector: Selector{TargetUserMessageID: "u1", ExpectedContent: stringPointer("one")}}}
			file := func(path, value string) {
				input.Files = append(input.Files, nativeFixtureFile{path, hex.EncodeToString([]byte(value))})
			}
			file(filepath.Join(backupDir, "before"), "old\n")
			file(filepath.Join(backupDir, "after"), "new\n")
			file(path, "new\n")
			input.Rows = []json.RawMessage{fixtureRow("u1", map[string]any{"a.txt": fixtureBackup("before")}, map[string]any{"a.txt": fixtureBackup("after")}, true)}
			switch name {
			case "conversation-only":
				input.ExecuteRequest.Mode = quoted("conversation")
			case "binary-backup":
				input.Files[0].Hex = hex.EncodeToString([]byte{0, 255, 1})
			case "create-and-delete":
				input.Rows = []json.RawMessage{fixtureRow("u1", map[string]any{"a.txt": fixtureBackup(nil), "gone.txt": fixtureBackup("before")}, map[string]any{"a.txt": fixtureBackup("after"), "gone.txt": fixtureBackup(nil)}, true)}
				input.ObservePaths = append(input.ObservePaths, filepath.Join(project, "gone.txt"))
			case "multi-turn-net-noop", "multi-turn-union":
				input.Messages = append(input.Messages, fixtureMessage("u2", "user", project, "two", ""), fixtureMessage("a2", "assistant", project, "done", ""))
				input.Rows = append(input.Rows, fixtureRow("u2", map[string]any{"a.txt": fixtureBackup("after")}, map[string]any{"a.txt": fixtureBackup("before")}, true))
				input.Files[2].Hex = hex.EncodeToString([]byte("old\n"))
				if name == "multi-turn-union" {
					input.Rows[1] = fixtureRow("u2", map[string]any{"a.txt": fixtureBackup("after"), "b.txt": fixtureBackup(nil)}, map[string]any{"a.txt": fixtureBackup("before"), "b.txt": fixtureBackup("after")}, true)
					file(filepath.Join(project, "b.txt"), "new\n")
					input.ObservePaths = append(input.ObservePaths, filepath.Join(project, "b.txt"))
				}
			case "missing-before":
				input.Rows = []json.RawMessage{fixtureRow("u1", map[string]any{"a.txt": map[string]any{"version": 1}}, map[string]any{"a.txt": fixtureBackup("after")}, true)}
			case "uncertain-uncovered":
				input.Rows = nil
				input.Messages = append(input.Messages, fixtureMessage("tool", "tool_use", project, []any{fixtureTool("write", "Write", map[string]any{"file_path": "other.txt", "content": "write"})}, ""))
			case "incomplete-transcript":
				input.Complete = false
			case "trim-failure-rolls-back":
				input.TrimError = "fixture trim failure"
			case "stop-drains-late-mutation":
				input.LateMessages = append(append([]protocol.JSONObject{}, input.Messages...), fixtureMessage("late", "tool_use", project, []any{fixtureTool("write", "Write", map[string]any{"file_path": "other.txt", "content": "late"})}, ""))
			case "stop-rechecks-prompt":
				input.LateMessages = []protocol.JSONObject{fixtureMessage("u1", "user", project, "changed", ""), fixtureMessage("a1", "assistant", project, "done", "")}
			case "invalid-mode":
				input.ExecuteRequest.Mode = quoted("files")
			case "latest-record-keeps-order":
				input.Messages = append(input.Messages, fixtureMessage("u2", "user", project, "two", ""), fixtureMessage("a2", "assistant", project, "done", ""))
				input.Rows = append(input.Rows, fixtureRow("u2", map[string]any{"a.txt": fixtureBackup("after")}, map[string]any{"a.txt": fixtureBackup("after")}, true), fixtureRow("u1", map[string]any{"a.txt": fixtureBackup("before")}, map[string]any{"a.txt": fixtureBackup("after")}, true))
			case "legacy-no-checkpoints":
				input.Rows = nil
			}
			input.PreviewRequests = []Selector{input.ExecuteRequest.Selector}
			output := runSourceRewindFixture(t, input)
			var expected struct {
				Previews []json.RawMessage `json:"previews"`
				Execute  struct {
					Result json.RawMessage `json:"result"`
					Calls  []string        `json:"calls"`
					Files  []observedFile  `json:"files"`
				} `json:"execute"`
				Capabilities struct{ Blocked, SecureReads []any } `json:"capabilities"`
			}
			if err := json.Unmarshal(output, &expected); err != nil {
				t.Fatal(err)
			}
			if len(expected.Capabilities.Blocked) > 0 || len(expected.Capabilities.SecureReads) > 0 {
				t.Fatalf("boundary %s", output)
			}
			service := fixtureService(t, input)
			preview, err := service.Preview(context.Background(), "fixture-session", input.ExecuteRequest.Selector)
			if err != nil {
				comparePublic(t, "preview", map[string]string{"error": err.Error()}, expected.Previews[0])
			} else {
				comparePublic(t, "preview", preview, expected.Previews[0])
			}
			calls := []string{}
			service.options.MessagesWithEvidence = func(context.Context, string) (Evidence, error) { return Evidence{input.Messages, input.Complete}, nil }
			service.options.StopSessionAndWait = func(context.Context, string) error {
				calls = append(calls, "stop")
				if input.LateMessages != nil {
					input.Messages = input.LateMessages
				}
				return nil
			}
			service.options.TrimSessionMessagesFrom = func(_ context.Context, _ string, target string) (TrimResult, error) {
				calls = append(calls, "trim:"+target)
				if input.TrimError != "" {
					return TrimResult{}, errors.New(input.TrimError)
				}
				index := 0
				for i, message := range input.Messages {
					if text(message, "id") == target {
						index = i
						break
					}
				}
				ids := []string{}
				for _, message := range input.Messages[index:] {
					ids = append(ids, text(message, "id"))
				}
				return TrimResult{len(ids), ids}, nil
			}
			mode, err := ParseMode(input.ExecuteRequest.Mode)
			var result ExecuteResult
			if err == nil {
				result, err = service.Execute(context.Background(), "fixture-session", input.ExecuteRequest.Selector, mode)
			}
			if err != nil {
				comparePublic(t, "execute", map[string]string{"error": err.Error()}, expected.Execute.Result)
			} else {
				comparePublic(t, "execute", result, expected.Execute.Result)
			}
			if !reflect.DeepEqual(calls, expected.Execute.Calls) {
				t.Errorf("calls %v vs %v", calls, expected.Execute.Calls)
			}
			actualFiles := observedNative(t, input.ObservePaths)
			if !reflect.DeepEqual(actualFiles, expected.Execute.Files) {
				t.Errorf("actual byte/mode states %#v vs %#v", actualFiles, expected.Execute.Files)
			}
		})
	}
}
