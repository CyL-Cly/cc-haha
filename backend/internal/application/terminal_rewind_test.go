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

func TestActualSourceRewindAliasesSkipCanonicalRecordsAndOpenExistingSelector(t *testing.T) {
	inputs := []string{"/rewind", "/rewind ignored arguments", "/checkpoint ignored alias arguments"}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Pinned Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-rewind.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	for _, input := range inputs {
		rows = append(rows, map[string]any{"input": input, "withContext": true}, map[string]any{"input": input, "withContext": false})
	}
	data, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_REWIND_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NO_COLOR=1", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}
	command.Stdin = bytes.NewReader(data)
	data, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source rewind %v %.5000s", err, data)
	}
	var source struct {
		Declaration struct {
			Name, Type, Description string
			Aliases                 []string
		}
		Rows []struct {
			Opened int
			Result struct {
				Messages    []json.RawMessage
				ShouldQuery bool
			}
		}
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatalf("actual Source rewind result %v %.5000s", err, data)
	}
	for index, row := range source.Rows {
		expected := 0
		if index%2 == 0 {
			expected = 1
		}
		if row.Opened != expected || len(row.Result.Messages) != 0 || row.Result.ShouldQuery {
			t.Fatalf("actual Source skip/callback row %d: %+v", index, row)
		}
	}
	if source.Declaration.Name != "rewind" || source.Declaration.Type != "local" || len(source.Declaration.Aliases) != 1 || source.Declaration.Aliases[0] != "checkpoint" {
		t.Fatal("actual Source declaration", source.Declaration)
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				n := calls.Add(1)
				if n == 2 && (!strings.Contains(string(body), "REWIND_ENTRY_ORIGINAL") || strings.Contains(string(body), "ignored arguments") || strings.Contains(string(body), "command-name")) {
					t.Error("skip command leaked or previous context lost", string(body))
				}
				response(w, "rewind-entry", "", nil, "REWIND_ENTRY_DONE")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Interactive = true
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			commands, unsubscribe := s.SubscribeCommands(nil)
			defer unsubscribe()
			for _, name := range []string{"rewind", "checkpoint"} {
				found := false
				for _, command := range commands {
					if command.Name == name && command.Kind == source.Declaration.Type && command.Description == source.Declaration.Description {
						found = true
					}
				}
				if !found {
					t.Fatal("original declaration consumer missing", name)
				}
			}
			if err = s.Submit(context.Background(), tui.Input{Text: "REWIND_ENTRY_ORIGINAL", TurnID: "rewind-original"}, nil); err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(s.process.Host.History())
			if err != nil {
				t.Fatal(err)
			}
			state := tui.NewState(nil, 110, 35)
			state.ActiveTurn = "rewind-local"
			state.Entries = []tui.Entry{{TurnID: "rewind-original", Kind: "user", Text: "REWIND_ENTRY_ORIGINAL"}, {TurnID: "rewind-local", Kind: "user", Text: input}}
			opened := 0
			if err = s.Submit(context.Background(), tui.Input{Text: input, TurnID: "rewind-local", Mode: "prompt"}, func(event tui.Event) error {
				if event.Kind == tui.MessageSelectorOpened {
					opened++
					if event.MessageSelector == nil || len(event.MessageSelector.Choices) != 1 || event.MessageSelector.Choices[0].Text != "REWIND_ENTRY_ORIGINAL" {
						t.Fatal("actual existing selector snapshot", event.MessageSelector)
					}
				}
				state.Apply(event)
				return nil
			}); err != nil {
				t.Fatal("ordinary rewind input", err)
			}
			after, err := json.Marshal(s.process.Host.History())
			if err != nil || !bytes.Equal(before, after) || calls.Load() != 1 || opened != 1 || state.MessageSelector == nil || len(state.Entries) != 1 {
				t.Fatal("actual canonical skip and visible existing selector", err, calls.Load(), opened, state.MessageSelector, state.Entries, string(before), string(after))
			}
			if err = s.Submit(context.Background(), tui.Input{Text: "Continue after selector cancellation", TurnID: "rewind-next"}, nil); err != nil || calls.Load() != 2 {
				t.Fatal("same Harness continuation", err, calls.Load())
			}
		})
	}
}
