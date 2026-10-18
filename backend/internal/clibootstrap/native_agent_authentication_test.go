package clibootstrap

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualOrdinaryAgentAuthenticationSnapshotParentWriteAndProcessLifetime(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreground", true: "background"}[background], func(t *testing.T) {
			addresses := make(chan string, 1)
			childContinue, parentContinue := make(chan struct{}), make(chan struct{})
			var parentCalls, childCalls atomic.Int32
			x := newTerminalMCPAuthFixtureWithOptions(t, terminalMCPAuthFixtureOptions{
				tools: []string{"Agent", "Read", "mcp__auth__authenticate", "mcp__auth__inspect"},
				prepare: func(x *terminalMCPAuthFixture) {
					x.f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "cli"
					rgData, err := os.ReadFile("/opt/homebrew/bin/rg")
					if err != nil {
						t.Fatal(err)
					}
					rgPath := filepath.Join(x.f.home, "bin", "rg")
					x.f.write(t, rgPath, string(rgData))
					if err := os.Chmod(rgPath, 0700); err != nil {
						t.Fatal(err)
					}
					x.f.n.Environment["CC_HAHA_RIPGREP_PATH"] = rgPath
					x.f.n.Environment["PATH"] = filepath.Dir(rgPath) + ":/usr/bin:/bin"

					globalPath := filepath.Join(x.f.config, ".claude.json")
					data, err := os.ReadFile(globalPath)
					if err != nil {
						t.Fatal(err)
					}
					global := mainObj(data)
					global.Set("projects", rawMain(map[string]any{x.f.n.CWD: map[string]any{"hasTrustDialogAccepted": true}}))
					x.f.write(t, globalPath, string(rawMain(global)))
					x.f.write(t, filepath.Join(x.f.cwd, ".claude", "agents", "auth-worker.md"), "---\nname: auth-worker\ndescription: Actual inherited authentication worker\ntools: Read, mcp__auth__authenticate\nmodel: inherit\n---\nAUTH_PSEUDO_CHILD_PRIVATE_MARKER")
					x.f.write(t, filepath.Join(x.f.cwd, "auth-child.txt"), "physical child Read after authentication")
				},
				model: func(x *terminalMCPAuthFixture, w http.ResponseWriter, r *http.Request, body []byte) {
					x.models.Add(1)
					request := mainObj(body)
					catalog := request.Get("tools")
					auth := bytes.Contains(catalog, []byte(`"name":"mcp__auth__authenticate"`))
					inspect := bytes.Contains(catalog, []byte(`"name":"mcp__auth__inspect"`))
					if bytes.Contains(request.Get("system"), []byte("AUTH_PSEUDO_CHILD_PRIVATE_MARKER")) {
						n := childCalls.Add(1)
						if !auth || inspect {
							t.Errorf("captured child catalogue changed at child POST%d: %s", n, catalog)
						}
						switch n {
						case 1:
							emitPlanFixture(w, 101, "mcp__auth__authenticate", "")
						case 2:
							parts := strings.Split(authenticationResultMessage(body), "\n\n")
							if len(parts) != 3 {
								t.Error("actual child authentication URL missing", string(body))
								emitPlanFixture(w, 102, "", "invalid auth result")
								return
							}
							addresses <- parts[1]
							if background {
								emitPlanFixture(w, 102, "", "Child finished before browser authentication")
								return
							}
							select {
							case <-childContinue:
							case <-r.Context().Done():
								return
							}
							emitNativeTaskTool(w, "child-read", "Read", map[string]any{"file_path": filepath.Join(x.f.cwd, "auth-child.txt")})
						case 3:
							if !bytes.Contains(body, []byte("physical child Read after authentication")) {
								t.Error("actual child Read missing from next HTTP")
							}
							emitPlanFixture(w, 103, "", "Captured authentication catalogue retained")
						default:
							t.Error("unexpected child model call", n)
							emitPlanFixture(w, int(n)+100, "", "unexpected child")
						}
						return
					}
					n := parentCalls.Add(1)
					switch n {
					case 1:
						emitNativeTaskTool(w, "parent-agent-auth", "Agent", map[string]any{"description": "Authenticate in child", "subagent_type": "auth-worker", "prompt": "Start authentication with the inherited MCP tool", "run_in_background": background})
					case 2:
						if background {
							select {
							case <-parentContinue:
							case <-r.Context().Done():
								return
							}
						} else if auth || !inspect {
							t.Error("foreground child did not update parent AppState", string(catalog))
						}
						emitPlanFixture(w, 2, "", "Parent Agent turn completed")
					case 3:
						if auth != background || inspect == background {
							t.Error("next actual parent catalogue differs from Source", background, string(catalog))
						}
						emitPlanFixture(w, 3, "", "Next human after child authentication")
					default:
						t.Error("unexpected parent model call", n)
						emitPlanFixture(w, int(n), "", "unexpected parent")
					}
				},
			})
			definitions, err := x.m.AgentDefinitions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, definition := range definitions.Active {
				found = found || definition.Name == "auth-worker"
			}
			if !found {
				t.Fatal("actual project Agent fixture absent", definitions.FailedFiles)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- x.m.RunTurn(ctx, x.user("Run the actual auth-worker Agent")) }()
			var address string
			select {
			case address = <-addresses:
			case err := <-done:
				t.Fatal("actual Agent failed before authentication URL", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if background {
				// Observe the real child registry until that actual child has
				// exited. Its returned URL must retain the process-owned flow.
				for {
					active := false
					receipt, err := x.m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
						rows, err := x.m.Controller.BackgroundTasksOnOwner(scope)
						for _, row := range rows {
							active = active || row.String("status") == "running"
						}
						return err
					})
					if err == nil {
						err = receipt.Wait()
					}
					if err != nil {
						t.Fatal(err)
					}
					if !active {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(time.Millisecond):
					}
				}
			}
			response, err := http.Get(terminalMCPAuthCallback(t, address))
			if err != nil {
				t.Fatal("child completion cancelled process OAuth", err)
			}
			_ = response.Body.Close()
			for {
				complete := false
				_ = filepath.WalkDir(filepath.Join(x.f.config, "Cache"), func(path string, entry os.DirEntry, err error) error {
					if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".jsonl") {
						data, _ := os.ReadFile(path)
						complete = complete || bytes.Contains(data, []byte("OAuth complete, reconnected with 1 tool(s)"))
					}
					return nil
				})
				if complete {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("actual process OAuth completion", ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			close(childContinue)
			close(parentContinue)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := x.m.RunTurn(ctx, x.user("Next human verifies actual captured parent catalogue")); err != nil {
				t.Fatal(err)
			}
			wantChild := int32(3)
			if background {
				wantChild = 2
			}
			if x.tokens.Load() != 1 || x.connects.Load() != 1 || x.calls.Load() != 0 || parentCalls.Load() != 3 || childCalls.Load() != wantChild {
				t.Fatal("actual Agent/OAuth/HTTP counts", x.tokens.Load(), x.connects.Load(), x.calls.Load(), parentCalls.Load(), childCalls.Load())
			}
		})
	}
}
