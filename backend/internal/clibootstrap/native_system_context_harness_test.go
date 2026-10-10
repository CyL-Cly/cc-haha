package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualASKUsesOriginalGitContextMemoAndCustomPromptGate(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-default-prompt", true: "explicit-custom-prompt"}[custom], func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"includeGitInstructions":true}`)
			f.n.Environment["PATH"] = "/usr/bin:/bin"
			f.n.Environment["ANTHROPIC_API_KEY"] = "offline-system-context-key"
			f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "true"
			f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "true"
			f.n.Environment["NODE_ENV"] = "production"
			f.n.Environment["GIT_CONFIG_NOSYSTEM"] = "1"
			f.n.Environment["GIT_CONFIG_GLOBAL"] = filepath.Join(f.home, "no-global")
			contextGit(t, f, "init", "-q", "--initial-branch=feature/real-ask")
			contextGit(t, f, "config", "user.name", "Private ASK Context")
			contextGit(t, f, "config", "user.email", "fixture@example.invalid")
			f.write(t, filepath.Join(f.cwd, "tracked.txt"), "original\n")
			contextGit(t, f, "add", ".")
			contextGit(t, f, "commit", "-qm", "original snapshot")
			var mu sync.Mutex
			requests := []json.RawMessage{}
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, data)
				round := len(requests)
				mu.Unlock()
				emitWorktreeModel(w, round, "", "", "actual normal reply")
			}))
			defer model.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
			prompt := ""
			if custom {
				prompt = "Explicit private custom prompt."
			}
			m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{Model: "deepseek-flash", SystemPrompt: prompt, Tools: []string{"Read"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			var snapshot protocol.JSONObject
			if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				var e error
				snapshot, e = m.nativeSystemContext(scope)
				return e
			}); err != nil {
				t.Fatal(err)
			}
			for turn := 0; turn < 2; turn++ {
				if turn == 1 {
					f.write(t, filepath.Join(f.cwd, "tracked.txt"), "mutated after original context snapshot\n")
				}
				input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Reply using this ordinary existing Agent turn."})})
				if err = m.RunTurn(ctx, input); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			observed := append([]json.RawMessage{}, requests...)
			mu.Unlock()
			if len(observed) != 2 {
				t.Fatal("inspection started extra inference", len(observed))
			}
			for _, request := range observed {
				var body protocol.JSONObject
				if err = json.Unmarshal(request, &body); err != nil {
					t.Fatal(err)
				}
				var sections []protocol.JSONObject
				if err = json.Unmarshal(body.Get("system"), &sections); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, section := range sections {
					if strings.Contains(section.String("text"), "This is the git status at the start of the conversation.") {
						found = true
						if !strings.Contains(section.String("text"), snapshot.String("gitStatus")) {
							t.Fatal("ASK lost actual Git snapshot")
						}
						if strings.Contains(section.String("text"), " M tracked.txt") {
							t.Fatal("Source memo was regenerated during next turn")
						}
					}
				}
				if found == custom {
					t.Fatal("custom prompt getSystemContext gate", custom, found)
				}
			}
			if err = m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
