package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestWholeSourceKeybindingsCommandExclusiveCreateOldFixtureEditorAndNextHarness(t *testing.T) {
	old := "{\"$schema\":\"old-private-schema\",\"future\":{\"keep\":true},\"bindings\":[]}\n"
	edited := "{\"future\":{\"keep\":true},\"bindings\":[{\"context\":\"Global\",\"bindings\":{\"f7\":\"app:toggleTodos\"}}]}\n"
	for _, row := range []struct {
		name, existing, edited, editorError string
		enabled                             bool
	}{{"new", "", "", "", true}, {"existing-unknown", old, "", "", true}, {"actual-edit", old, edited, "", true}, {"new-editor-error", "", "", "Owned-editor exited with code 17", true}, {"old-editor-error", old, "", "Owned-editor exited with code 17", true}, {"gate-false", "", "", "", false}} {
		t.Run(row.name, func(t *testing.T) {
			bun := os.Getenv("CC_HAHA_SOURCE_BUN")
			if bun == "" {
				t.Fatal("Pinned Source Bun required")
			}
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				body, _ := io.ReadAll(request.Body)
				calls.Add(1)
				if !strings.Contains(string(body), "/keybindings") {
					t.Error("canonical local command lost from actual next request")
				}
				response(w, "keybindings-next", "", nil, "KEYBINDINGS_NEXT_DONE")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Interactive = true
			o.KeybindingCustomizationEnabled = func() bool { return row.enabled }
			editor := filepath.Join(o.Native.Home, "owned-editor")
			o.Environment["EDITOR"], o.Native.Environment["EDITOR"] = editor, editor
			home := o.Native.Home
			path := filepath.Join(home, ".claude", "keybindings.json")
			input := map[string]any{"home": home, "enabled": row.enabled}
			if row.existing != "" {
				input["existing"] = row.existing
			}
			if row.edited != "" {
				input["edited"] = row.edited
			}
			input["editorError"] = row.editorError
			data, _ := json.Marshal(input)
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-keybindings.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_KEYBINDINGS_COMMAND_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NO_COLOR=1", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			command.Stdin = bytes.NewReader(data)
			data, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("whole original keybindings command %v %.5000s", err, data)
			}
			var source struct {
				Result  struct{ Type, Value string }
				Content *string
				Editors []struct{ Path, Before string }
				Gates   [][]json.RawMessage
			}
			if err = json.Unmarshal(data, &source); err != nil || source.Result.Type != "text" || len(source.Gates) != 1 || string(source.Gates[0][0]) != `"tengu_keybinding_customization_release"` || string(source.Gates[0][1]) != "false" {
				t.Fatalf("original actual result %v %.5000s", err, data)
			}
			if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if row.existing != "" {
				if err = os.WriteFile(path, []byte(row.existing), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			commands, unsubscribe := s.SubscribeCommands(nil)
			defer unsubscribe()
			found := false
			for _, command := range commands {
				if command.Name == "keybindings" && command.Kind == "local" {
					found = true
				}
			}
			if found != row.enabled {
				t.Fatal("actual trusted gate metadata", found, row.enabled)
			}
			if !row.enabled {
				if _, err := os.Stat(path); !os.IsNotExist(err) || source.Content != nil || len(source.Editors) != 0 {
					t.Fatal("disabled customization wrote file or opened editor", err, source.Content, source.Editors)
				}
				return
			}
			opened := 0
			if err = s.Submit(context.Background(), tui.Input{Text: "/keybindings", TurnID: "keybindings-local", Mode: "prompt"}, func(event tui.Event) error {
				if event.Kind == tui.FileEditorRequested {
					opened++
					if event.FileEditor == nil || event.FileEditor.Path != path || event.FileEditor.Editor != editor || event.FileEditor.Directory != o.Native.CWD {
						t.Fatal("actual terminal renderer handoff", event.FileEditor)
					}
					before, err := os.ReadFile(path)
					if err != nil || len(source.Editors) != 1 || string(before) != source.Editors[0].Before {
						t.Fatal("exclusive create/template/old raw bytes before editor", err, string(before), source.Editors)
					}
					if row.edited != "" {
						if err = os.WriteFile(path, []byte(row.edited), 0600); err != nil {
							t.Fatal(err)
						}
					}
					event.FileEditor.Reply <- tui.FileEditorResult{Error: row.editorError}
				}
				return nil
			}); err != nil {
				t.Fatal("ordinary keybindings input", err)
			}
			actual, readErr := os.ReadFile(path)
			if readErr != nil || source.Content == nil || string(actual) != *source.Content || opened != 1 || calls.Load() != 0 {
				t.Fatal("original physical file and actual local entry", readErr, string(actual), source.Content, opened, calls.Load())
			}
			matched := false
			for _, record := range s.process.Host.History() {
				if record.Record.String("type") == "system" && record.Record.String("content") == "<local-command-stdout>"+source.Result.Value+"</local-command-stdout>" {
					matched = true
				}
			}
			if !matched {
				t.Fatal("actual Source completion absent from canonical records", source.Result.Value)
			}
			if err = s.Submit(context.Background(), tui.Input{Text: "Continue after keybindings editing", TurnID: "keybindings-next"}, nil); err != nil || calls.Load() != 1 {
				t.Fatal("same actual next Harness", err, calls.Load())
			}
		})
	}
}
