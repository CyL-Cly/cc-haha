package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"
)

func TestActualPublicSDKBackgroundHookIsolationColdAgentResumeAndMissingPath(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "retained"
		if missing {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			f := worktreeMainFixture(t)
			f.n.Environment["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
			var lock sync.Mutex
			parentBodies, childBodies := [][]byte{}, [][]byte{}
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			var id string
			resumed := false
			firstParent, coldParent := 0, 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				parent := bytes.Contains(body, []byte("SDK_PARENT_BG_629")) || bytes.Contains(body, []byte("SDK_PARENT_COLD_629"))
				lock.Lock()
				cold := resumed
				if parent {
					parentBodies = append(parentBodies, body)
				} else {
					childBodies = append(childBodies, body)
				}
				n := len(childBodies)
				if parent {
					if cold {
						coldParent++
						n = coldParent
					} else {
						firstParent++
						n = firstParent
					}
				}
				lock.Unlock()
				if parent {
					if !cold {
						switch n {
						case 1:
							emitWorktreeModel(w, n, "Agent", `{"prompt":"SDK_CHILD_BG_629: read hook-owned.txt","description":"actual SDK background isolation","subagent_type":"general-purpose","isolation":"worktree","run_in_background":true}`, "")
						case 2:
							match := regexp.MustCompile(`agentId: ([a-zA-Z0-9-]+)`).FindSubmatch(body)
							if len(match) != 2 {
								t.Error("actual async ID missing")
								return
							}
							lock.Lock()
							id = string(match[1])
							lock.Unlock()
							unblock()
							emitWorktreeModel(w, n, "TaskOutput", string(rawMain(map[string]any{"task_id": string(match[1]), "block": true, "timeout": 10000})), "")
						case 3:
							emitWorktreeModel(w, n, "", "", "SDK_BACKGROUND_COMPLETE_629")
						case 4:
							if !bytes.Contains(body, []byte("task-notification")) {
								t.Error("extra parent request was not the real background notification")
							}
							emitWorktreeModel(w, n, "", "", "SDK_NOTIFICATION_COMPLETE_629")
						default:
							t.Errorf("unexpected first-phase parent request %d", n)
						}
					} else {
						lock.Lock()
						saved := id
						lock.Unlock()
						switch n {
						case 1:
							emitWorktreeModel(w, n, "SendMessage", string(rawMain(map[string]any{"to": saved, "summary": "resume actual isolated reader", "message": "SDK_CHILD_COLD_629: read current hook-owned.txt"})), "")
						case 2:
							emitWorktreeModel(w, n, "TaskOutput", string(rawMain(map[string]any{"task_id": saved, "block": true, "timeout": 10000})), "")
						case 3:
							emitWorktreeModel(w, n, "", "", "SDK_COLD_COMPLETE_629")
						case 4:
							if !bytes.Contains(body, []byte("task-notification")) {
								t.Error("extra cold parent request was not the real background notification")
							}
							emitWorktreeModel(w, n, "", "", "SDK_COLD_NOTIFICATION_COMPLETE_629")
						default:
							t.Errorf("unexpected cold parent request %d", n)
						}
					}
					return
				}
				switch n {
				case 1:
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					emitWorktreeModel(w, n, "Read", `{"file_path":"hook-owned.txt"}`, "")
				case 2:
					emitWorktreeModel(w, n, "", "", "SDK_FIRST_CHILD_DONE_629")
				case 3:
					if !cold {
						t.Error("unexpected child before cold restart")
					}
					path := "hook-owned.txt"
					if missing {
						path = "src/tracked.txt"
					}
					emitWorktreeModel(w, n, "Read", string(rawMain(map[string]string{"file_path": path})), "")
				case 4:
					emitWorktreeModel(w, n, "", "", "SDK_COLD_CHILD_DONE_629")
				default:
					t.Errorf("unexpected actual child request %d", n)
				}
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			disabled := false
			var diagnostics bytes.Buffer
			run := func(resume string, prompt string) (*MainPrint, *sdkWorktreePeerWriter) {
				reader, writer := io.Pipe()
				t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
				output := &sdkWorktreePeerWriter{input: writer, home: f.home, oneTurn: true}
				options := MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState(), Resume: resume}, Native: f.n.agentScopedBoundary(f.cwd), PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: output, Error: &diagnostics}
				m, e := NewMainPrint(options)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
				go func() {
					_, _ = writer.Write([]byte(`{"type":"control_request","request_id":"init-cold629","request":{"subtype":"initialize","hooks":{"WorktreeCreate":[{"hookCallbackIds":["create-cold629"]}],"WorktreeRemove":[{"hookCallbackIds":["remove-cold629"]}]}}}` + "\n" + `{"type":"user","message":{"role":"user","content":` + string(quoteMain(prompt)) + `}}` + "\n"))
				}()
				if _, e = m.Run(ctx); e != nil {
					t.Fatal("actual public SDK background/cold consumer", e, diagnostics.String())
				}
				return m, output
			}
			first, output := run("", "SDK_PARENT_BG_629: run one isolated background reader and wait for actual completion")
			output.mu.Lock()
			tree := output.first
			created := len(output.frames)
			output.mu.Unlock()
			if tree == "" {
				t.Fatal("no actual SDK callback path")
			}
			if _, e := os.Stat(tree); e != nil {
				t.Fatal("background hook tree must remain", e)
			}
			lock.Lock()
			savedID := id
			lock.Unlock()
			if savedID == "" {
				t.Fatal("no real asynchronous agent identity")
			}
			paths, e := filepath.Glob(filepath.Join(f.config, "projects", "*", first.Controller.SessionID(), "subagents", "agent-"+savedID+".jsonl"))
			if e != nil || len(paths) != 1 {
				t.Fatal("actual persisted subagent", paths, e)
			}
			before, e := os.ReadFile(paths[0])
			if e != nil || !bytes.Contains(before, []byte("SDK_ACTUAL_PHYSICAL_629")) {
				t.Fatal("actual background child transcript", e, string(before))
			}
			sourceFixture, _ := filepath.Abs("../../testdata/native-agent-isolation-transcript.fixture.ts")
			source := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", sourceFixture)
			source.Dir = f.home
			source.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_AGENT_TRANSCRIPT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1"}
			source.Stdin = bytes.NewReader(rawMain(map[string]string{"home": f.home, "parentCwd": f.cwd, "worktree": tree, "sid": first.Controller.SessionID(), "agentId": savedID}))
			actualSource, e := source.CombinedOutput()
			if e != nil || !bytes.Contains(actualSource, []byte(paths[0])) {
				t.Fatal("real Source parent transcript root", e, string(actualSource), paths[0])
			}
			if missing {
				if e = os.RemoveAll(tree); e != nil {
					t.Fatal(e)
				}
			} else {
				f.write(t, filepath.Join(tree, "hook-owned.txt"), "SDK_COLD_CURRENT_FILE_629")
			}
			lock.Lock()
			resumed = true
			lock.Unlock()
			second, coldOutput := run(first.Controller.SessionID(), "SDK_PARENT_COLD_629: resume the saved isolated child and read current workspace")
			coldOutput.mu.Lock()
			defer coldOutput.mu.Unlock()
			for _, event := range coldOutput.frames {
				request, _ := event.Object("request")
				if request.String("subtype") == "hook_callback" {
					t.Fatal("cold resume recreated a stored hook worktree", string(rawMain(event)), created)
				}
			}
			if second.Controller.SessionID() != first.Controller.SessionID() {
				t.Fatal("cold parent identity changed")
			}
			after, e := os.ReadFile(paths[0])
			if e != nil || !bytes.HasPrefix(after, before) {
				t.Fatal("cold sidechain rewrote stored history", e)
			}
			lock.Lock()
			defer lock.Unlock()
			if len(parentBodies) != 8 || len(childBodies) != 4 {
				t.Fatal("actual background/cold request counts", len(parentBodies), len(childBodies), diagnostics.String())
			}
			cwd, content := tree, "SDK_COLD_CURRENT_FILE_629"
			if missing {
				cwd, content = f.cwd, "tracked fixture"
			}
			if !bytes.Contains(childBodies[2], []byte("Working directory: "+cwd)) || !bytes.Contains(childBodies[2], []byte("SDK_FIRST_CHILD_DONE_629")) || !bytes.Contains(childBodies[3], []byte(content)) {
				t.Fatal("actual cold restored cwd/history/current physical Read", string(childBodies[2]), string(childBodies[3]))
			}
		})
	}
}
