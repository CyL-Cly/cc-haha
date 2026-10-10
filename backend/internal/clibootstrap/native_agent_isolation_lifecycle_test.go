package clibootstrap

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
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The default Agent tool must retain the hook-created cwd across an independent
// background task and a cold parent restore. The second case exercises Source's
// missing-directory fallback, rather than supplying a replacement cwd in a test.
func TestActualDefaultBackgroundIsolationParentCancelColdResumeAndMissingDirectory(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "retained"
		if missing {
			name = "missing-falls-back"
		}
		t.Run(name, func(t *testing.T) {
			f := worktreeMainFixture(t)
			tree := filepath.Join(f.home, "background-hook-tree")
			journal := filepath.Join(f.home, "create-input.json")
			script := filepath.Join(f.home, "create-background.py")
			f.write(t, script, "import sys,json,os\nvalue=json.load(sys.stdin)\njson.dump(value,open("+string(quoteMain(journal))+",'w'))\nos.makedirs("+string(quoteMain(tree))+",exist_ok=True)\nopen(os.path.join("+string(quoteMain(tree))+",'hook-owned.txt'),'w').write('P606_BACKGROUND_PHYSICAL_FILE')\nprint("+string(quoteMain(tree))+")\n")
			f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(map[string]any{"enabledPlugins": map[string]bool{"sample@mkt": false}, "agentTeamsEnabled": false, "alwaysThinkingEnabled": false, "hooks": map[string]any{"WorktreeCreate": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + script}}}}}})))
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			childEntered, release := make(chan struct{}), make(chan struct{})
			parentOutput := make(chan struct{})
			var parentOutputOnce sync.Once
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			var mu sync.Mutex
			parentBodies, childBodies := [][]byte{}, [][]byte{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				parent := bytes.Contains(raw, []byte("P606_PARENT_BACKGROUND_REQUEST"))
				mu.Lock()
				if parent {
					parentBodies = append(parentBodies, raw)
				} else {
					childBodies = append(childBodies, raw)
				}
				n := len(childBodies)
				if parent {
					n = len(parentBodies)
				}
				mu.Unlock()
				if parent {
					if n == 1 {
						emitWorktreeModel(w, 1, "Agent", `{"prompt":"P606_CHILD_BACKGROUND_REQUEST: read hook-owned.txt","description":"independent isolated reader","subagent_type":"general-purpose","isolation":"worktree","run_in_background":true}`, "")
					} else if n == 2 {
						emitWorktreeModel(w, 2, "", "", "P606_PARENT_FINISHED_BEFORE_CHILD")
					} else {
						t.Errorf("unexpected parent model request %d", n)
					}
					return
				}
				switch n {
				case 1:
					close(childEntered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					emitWorktreeModel(w, n, "Read", `{"file_path":"hook-owned.txt"}`, "")
				case 2:
					emitWorktreeModel(w, n, "", "", "P606_FIRST_CHILD_FINISHED")
				case 3:
					path := "hook-owned.txt"
					if missing {
						path = "src/tracked.txt"
					}
					emitWorktreeModel(w, n, "Read", string(rawMain(map[string]string{"file_path": path})), "")
				case 4:
					emitWorktreeModel(w, n, "", "", "P606_COLD_CHILD_FINISHED")
				default:
					t.Errorf("unexpected child model request %d", n)
				}
			}))
			defer func() { unblock(); peer.Close() }()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			disabled := false
			options := MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func(data []byte) error {
				if bytes.Contains(data, []byte("P606_PARENT_FINISHED_BEFORE_CHILD")) {
					parentOutputOnce.Do(func() { close(parentOutput) })
				}
				return nil
			}}
			m, err := NewMainPrint(options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			parentSnapshot := m.state.Snapshot()
			turn, cancelTurn := context.WithCancel(ctx)
			defer cancelTurn()
			input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "P606_PARENT_BACKGROUND_REQUEST: create one isolated background reader"})})
			turnResult := make(chan error, 1)
			go func() { turnResult <- m.RunTurn(turn, input) }()
			select {
			case <-childEntered:
			case <-ctx.Done():
				t.Fatal("actual background child never reached its physical model peer")
			}
			select {
			case <-parentOutput:
			case <-ctx.Done():
				t.Fatal("actual parent final output never reached its real output sink")
			}
			// RunTurn deliberately drains independent background tasks. Cancel its
			// observer only after the real parent final output, while the child peer
			// remains held, then verify the separate child continues below.
			cancelTurn()
			select {
			case err = <-turnResult:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("cancelled parent observer did not finish its background drain")
			}
			paths, err := filepath.Glob(filepath.Join(f.config, "projects", "*", parentSnapshot.SessionID, "subagents", "agent-*.meta.json"))
			if err != nil || len(paths) != 1 {
				t.Fatal("actual child metadata", paths, err)
			}
			id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(paths[0]), "agent-"), ".meta.json")
			fixture, _ := filepath.Abs("../../testdata/native-agent-isolation-transcript.fixture.ts")
			original := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
			original.Dir = f.home
			original.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_AGENT_TRANSCRIPT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1"}
			original.Stdin = bytes.NewReader(rawMain(map[string]string{"home": f.home, "parentCwd": parentSnapshot.OriginalCWD, "worktree": tree, "sid": parentSnapshot.SessionID, "agentId": id}))
			originalResult, sourceError := original.CombinedOutput()
			if sourceError != nil {
				t.Fatal("actual Source transcript location", sourceError, string(originalResult))
			}
			var location struct {
				Before, After string
				Inside        struct{ Cwd, Original, Project, Path, Sid string }
			}
			if sourceError = json.Unmarshal(originalResult, &location); sourceError != nil {
				t.Fatal(sourceError, string(originalResult))
			}
			physicalPath := strings.TrimSuffix(paths[0], ".meta.json") + ".jsonl"
			if location.Before != physicalPath || location.After != physicalPath || location.Inside.Path != physicalPath || location.Inside.Cwd != tree || location.Inside.Original != parentSnapshot.OriginalCWD || location.Inside.Project != parentSnapshot.ProjectRoot || location.Inside.Sid != parentSnapshot.SessionID {
				t.Fatal("actual Source parent transcript root diverged", location, physicalPath)
			}
			unblock()
			entry := waitActualAgent(t, m, id)
			if entry.Data.String("status") != "completed" {
				t.Fatal("cancelled parent turn killed its independent isolated child", rawMain(entry.Data))
			}
			before, err := os.ReadFile(strings.TrimSuffix(paths[0], ".meta.json") + ".jsonl")
			if err != nil || !bytes.Contains(before, []byte("P606_BACKGROUND_PHYSICAL_FILE")) {
				t.Fatal("actual child Read/transcript missing", err, string(before))
			}
			if err = m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			old := time.Unix(100, 0)
			if missing {
				if err = os.RemoveAll(tree); err != nil {
					t.Fatal(err)
				}
			} else if err = os.Chtimes(tree, old, old); err != nil {
				t.Fatal(err)
			}
			options.Native = f.n.agentScopedBoundary(f.cwd)
			options.Arguments.Resume = parentSnapshot.SessionID
			reopened, err := NewMainPrint(options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.ShutdownSession(context.Background()) })
			if err = reopened.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			response, err := reopened.SendAgentMessage(ctx, id, "P606_COLD_RESUME_PROMPT: read the current workspace file")
			if err != nil || response.String("delivery") != "resumed" {
				actualPath := ""
				_ = reopened.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					actualPath, _, _, _ = reopened.Controller.AgentTranscriptSourceOnOwner(scope, id)
					return nil
				})
				t.Fatal("cold real control consumer", response, err, "actualPath", actualPath, "originalPath", strings.TrimSuffix(paths[0], ".meta.json")+".jsonl", "parentSID", parentSnapshot.SessionID, "reopenedSID", reopened.Controller.SessionID(), "parentState", parentSnapshot, "reopenedState", reopened.state.Snapshot())
			}
			entry = waitActualAgent(t, reopened, id)
			if entry.Data.String("status") != "completed" {
				t.Fatal("cold isolated child failed", rawMain(entry.Data))
			}
			if err = reopened.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(parentBodies) != 2 || len(childBodies) != 4 {
				t.Fatal("unexpected real request counts", len(parentBodies), len(childBodies))
			}
			cwd := tree
			content := "P606_BACKGROUND_PHYSICAL_FILE"
			if missing {
				cwd, content = f.cwd, "tracked fixture"
			}
			if !bytes.Contains(childBodies[2], []byte("Working directory: "+cwd)) || !bytes.Contains(childBodies[2], []byte("P606_FIRST_CHILD_FINISHED")) || !bytes.Contains(childBodies[2], []byte("P606_COLD_RESUME_PROMPT")) || !bytes.Contains(childBodies[3], []byte(content)) {
				t.Fatal("cold cwd/history/Read did not flow through the actual MainPrint", string(childBodies[2]), string(childBodies[3]))
			}
			after, err := os.ReadFile(strings.TrimSuffix(paths[0], ".meta.json") + ".jsonl")
			if err != nil || !bytes.HasPrefix(after, before) {
				t.Fatal("cold restore rewrote the original sidechain", err)
			}
			if !missing {
				stat, err := os.Stat(tree)
				if err != nil || !stat.ModTime().After(old) {
					t.Fatal("retained worktree was not touched/kept", err)
				}
			}
			if current := reopened.state.Snapshot(); current.CWD != parentSnapshot.CWD || current.OriginalCWD != parentSnapshot.OriginalCWD || current.ProjectRoot != parentSnapshot.ProjectRoot {
				t.Fatal("background/resume retargeted its parent")
			}
		})
	}
}
