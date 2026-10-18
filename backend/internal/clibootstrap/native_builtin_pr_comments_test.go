package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualPRCommentsSlashUsesOriginalPromptRealBashAndCanonicalResults(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	bin := filepath.Join(f.home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	gh := filepath.Join(bin, "gh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/gh-observed.txt\"\ncase \"$*\" in\n'pr view --json number,headRepository') printf '%s\\n' '{\"number\":23,\"headRepository\":{\"name\":\"repo\",\"owner\":{\"login\":\"private\"}}}' ;;\n'api /repos/private/repo/issues/23/comments') printf '%s\\n' '[{\"user\":{\"login\":\"fixture-reviewer\"},\"body\":\"REAL_PR_ISSUE_COMMENT_3C7\"}]' ;;\n'api /repos/private/repo/pulls/23/comments') printf '%s\\n' '[{\"user\":{\"login\":\"fixture-reviewer\"},\"body\":\"REAL_PR_REVIEW_COMMENT_3C7\",\"path\":\"src/example.go\",\"line\":7,\"diff_hunk\":\"@@ -1 +1 @@\\n+checked\"}]' ;;\n*) exit 9 ;;\nesac\n"
	if err := os.WriteFile(gh, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	f.n.Environment["PATH"] = bin + string(os.PathListSeparator) + "/usr/bin:/bin"
	// Bash runs as a login shell, as in Source. macOS /etc/profile can replace
	// the child's PATH, so install the executable fixture in this private HOME's
	// login profile instead of accidentally resolving the machine's real gh.
	f.write(t, filepath.Join(f.home, ".bash_profile"), "export PATH='"+strings.ReplaceAll(f.n.Environment["PATH"], "'", "'\\''")+"'\n")
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := int(calls.Add(1))
		if n == 1 {
			if !bytes.Contains(body, []byte("Your task is to fetch and display comments from a GitHub pull request.")) || !bytes.Contains(body, []byte("Additional user input: fixture PR 23")) {
				t.Errorf("original native command prompt missing %.1800s", body)
			}
			emitWorktreeModel(w, n, "Bash", `{"command":"gh pr view --json number,headRepository; gh api /repos/private/repo/issues/23/comments; gh api /repos/private/repo/pulls/23/comments","description":"fetch actual private PR fixture"}`, "")
			return
		}
		if n != 2 || !bytes.Contains(body, []byte("REAL_PR_ISSUE_COMMENT_3C7")) || !bytes.Contains(body, []byte("REAL_PR_REVIEW_COMMENT_3C7")) {
			var request struct{ Messages []json.RawMessage }
			_ = json.Unmarshal(body, &request)
			var last json.RawMessage
			if len(request.Messages) > 0 {
				last = request.Messages[len(request.Messages)-1]
			}
			t.Errorf("physical Bash GH output did not reach actual model turn%d last message %.4000s", n, last)
		}
		emitWorktreeModel(w, n, "", "", "## Comments\n- @fixture-reviewer src/example.go#7:\n  > REAL_PR_REVIEW_COMMENT_3C7\n- @fixture-reviewer:\n  > REAL_PR_ISSUE_COMMENT_3C7")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual /pr-comments main Harness", Tools: []string{"Bash"}, PermissionMode: "bypassPermissions", MaxTurns: 4, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "/pr-comments fixture PR 23"})})
	if err = m.RunTurn(ctx, input); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if calls.Load() != 2 {
		t.Fatal("not actual native command Bash→model turn", calls.Load(), diagnostics.String())
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	observed, err := os.ReadFile(filepath.Join(f.home, "gh-observed.txt"))
	if err != nil || string(observed) != "pr view --json number,headRepository\napi /repos/private/repo/issues/23/comments\napi /repos/private/repo/pulls/23/comments\n" {
		t.Fatal("actual executable dependency was not called", err, string(observed))
	}
	files, err := filepath.Glob(filepath.Join(f.config, "projects", "*", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	disk, err := os.ReadFile(files[0])
	if err != nil || !bytes.Contains(disk, []byte("REAL_PR_ISSUE_COMMENT_3C7")) || !bytes.Contains(disk, []byte("REAL_PR_REVIEW_COMMENT_3C7")) || !bytes.Contains(disk, []byte("<command-name>/pr-comments")) {
		t.Fatal("actual canonical command/tool/report missing", err)
	}
}
