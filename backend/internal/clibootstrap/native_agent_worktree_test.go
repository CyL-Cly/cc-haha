package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualSharedAgentWorktreeChildCWDTranscriptAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		dirty, configured, noGit, failed bool
	}{{name: "dirty-explicit", dirty: true}, {name: "clean-configured", configured: true}, {name: "no-git-fallback", noGit: true}, {name: "failed-child-cleanup", failed: true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := worktreeMainFixture(t)
			f.n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
			f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
			_ = os.Remove(filepath.Join(f.cwd, ".worktreeinclude"))
			if tc.noGit {
				if e := os.RemoveAll(filepath.Join(f.cwd, ".git")); e != nil {
					t.Fatal(e)
				}
			}
			agentType := "general-purpose"
			if tc.configured {
				agentType = "isolated-worker"
				f.write(t, filepath.Join(f.config, "agents", "isolated.md"), "---\nname: isolated-worker\ndescription: configured isolation\ntools: Read, Write\nisolation: worktree\n---\nComplete the task in the actual worktree.")
			}
			var mu sync.Mutex
			round := 0
			var bodies [][]byte
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				mu.Lock()
				round++
				n := round
				bodies = append(bodies, data)
				mu.Unlock()
				input := `{"prompt":"Complete the child task","description":"isolated task","subagent_type":"` + agentType + `"}`
				if !tc.configured {
					input = strings.TrimSuffix(input, "}") + `,"isolation":"worktree"}`
				}
				switch n {
				case 1:
					emitWorktreeModel(w, n, "Agent", input, "")
				case 2:
					if tc.failed {
						http.Error(w, "private child failure", 500)
						return
					}
					if tc.dirty {
						emitWorktreeModel(w, n, "Write", `{"file_path":"agent-proof.txt","content":"ACTUAL_CHILD_ISOLATION"}`, "")
					} else {
						emitWorktreeModel(w, n, "Read", `{"file_path":"src/tracked.txt"}`, "")
					}
				case 3:
					emitWorktreeModel(w, n, "", "", "child actual task complete")
				case 4:
					emitWorktreeModel(w, n, "", "", "parent actual task complete")
				default:
					t.Errorf("unexpected model call %d", n)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer model.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
			disabled := false
			var diagnostics bytes.Buffer
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private parent prompt", Tools: []string{"Agent,Read,Write"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 8, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			if e = m.StartSession(context.Background()); e != nil {
				t.Fatal(e)
			}
			parent := m.state.Snapshot()
			input := protocol.NewJSONObject()
			_ = input.UnmarshalJSON([]byte(`{"type":"user","uuid":"agent-worktree-parent","message":{"role":"user","content":"run the isolated child"}}`))
			if e = m.RunTurn(context.Background(), input); e != nil {
				t.Fatalf("shared child %v %s", e, diagnostics.String())
			}
			if e = m.ShutdownSession(context.Background()); e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			defer mu.Unlock()
			expectedRequests := 4
			if tc.failed {
				expectedRequests = 3
			}
			if round != expectedRequests {
				t.Fatalf("modelcalls%d diagnostics%s final%s", round, diagnostics.String(), bodies[len(bodies)-1])
			}
			snapshot := m.state.Snapshot()
			if snapshot.CWD != parent.CWD || snapshot.OriginalCWD != parent.OriginalCWD || snapshot.ProjectRoot != parent.ProjectRoot || m.worktreeState.current != nil {
				t.Fatal("parent changed", parent, snapshot)
			}
			paths, e := filepath.Glob(filepath.Join(f.config, "projects", "*", parent.SessionID, "subagents", "agent-*.meta.json"))
			if e != nil || len(paths) != 1 {
				t.Fatal("child transcript scope", paths, e)
			}
			raw, e := os.ReadFile(paths[0])
			if e != nil {
				t.Fatal(e)
			}
			metadata := protocol.NewJSONObject()
			_ = metadata.UnmarshalJSON(raw)
			agentID := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(paths[0]), "agent-"), ".meta.json")
			physical := filepath.Join(f.cwd, ".claude", "worktrees", "agent-"+agentID[:8])
			expectedCWD := physical
			if tc.noGit {
				expectedCWD = f.cwd
			}
			if !bytes.Contains(bodies[1], []byte("Working directory: "+expectedCWD)) {
				t.Fatalf("child environment cwd %s", bodies[1])
			}
			if tc.dirty {
				output, e := os.ReadFile(filepath.Join(physical, "agent-proof.txt"))
				if e != nil || string(output) != "ACTUAL_CHILD_ISOLATION" {
					t.Fatal("child actual relative write", string(output), e)
				}
				if _, e = os.Stat(filepath.Join(f.cwd, "agent-proof.txt")); !os.IsNotExist(e) {
					t.Fatal("parent relative write leak", e)
				}
				if metadata.String("worktreePath") != physical || !bytes.Contains(bodies[3], []byte("worktreePath: "+physical)) {
					t.Fatal("kept worktree metadata/result", metadata, string(bodies[3]))
				}
			} else {
				if metadata.Get("worktreePath") != nil {
					t.Fatal("clean metadata still targets removed dir", metadata)
				}
				if _, e = os.Stat(physical); !os.IsNotExist(e) {
					t.Fatal("clean worktree remains", e)
				}
				if tc.noGit && !bytes.Contains(bodies[3], []byte("worktree isolation was skipped (the workspace is not a git repository")) {
					t.Fatal("fallback falsely claims isolation", string(bodies[3]))
				}
			}
			transcript := strings.TrimSuffix(paths[0], ".meta.json") + ".jsonl"
			data, e := os.ReadFile(transcript)
			if e != nil || !bytes.Contains(data, []byte(`"isSidechain":true`)) {
				t.Fatal("actual child sidechain absent", e, string(data))
			}
			var records []protocol.JSONObject
			for _, line := range bytes.Split(data, []byte{'\n'}) {
				var row protocol.JSONObject
				if row.UnmarshalJSON(line) == nil && (row.String("type") == "assistant" || row.String("type") == "user") {
					records = append(records, row)
				}
			}
			if !tc.failed && len(records) < 3 {
				t.Fatal("child transcript incomplete", len(records), string(data))
			}
		})
	}
}
