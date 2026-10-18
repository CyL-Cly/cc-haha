package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceAgentTaskUpdateForegroundParentViewAndBackgroundNoop(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual original Agent Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-task-list-agent-context.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_AGENT_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=36"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": []any{map[string]bool{"async": false}, map[string]bool{"async": true}, map[string]bool{"async": false, "promote": true}}})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Actual runAgent/context/TaskUpdate/onChangeAppState", err, string(output))
	}
	var original struct {
		Results []struct {
			ExpandedView        string
			Global              protocol.JSONObject
			Update              protocol.JSONObject
			Tools               []string
			InitialTools        []string
			UnknownToolMessages []protocol.JSONObject
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &original); err != nil || len(original.Results) != 3 || len(original.Caps.Blocked) > 0 || len(original.Caps.SecureReads) > 0 {
		t.Fatal(err, string(output))
	}
	for index, profile := range []struct {
		name                string
		background, promote bool
	}{{name: "foreground"}, {name: "background", background: true}, {name: "promoted", promote: true}} {
		t.Run(profile.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var parents, children atomic.Int32
			var nextHuman atomic.Bool
			childUpdated := make(chan struct{})
			childEntered, childReleased := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(childReleased) }) }
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodHead {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				body, _ := io.ReadAll(request.Body)
				if strings.Contains(string(mainObj(body).Get("system")), "private actual parent harness") {
					n := parents.Add(1)
					if bytes.Contains(body, []byte("TASK_AGENT_NEXT_HUMAN")) {
						nextHuman.Store(true)
						if !bytes.Contains(body, []byte("parent-task-agent")) || !bytes.Contains(body, []byte("Run the genuine task Agent")) {
							t.Error("Next same-Harness human lost actual Agent history", string(body))
						}
					}
					switch n {
					case 1:
						emitNativeTaskTool(w, "parent-task-agent", "Agent", map[string]any{"description": "Actual ordinary task child", "prompt": "Try TaskUpdate for missing task 99", "subagent_type": "general-purpose", "run_in_background": profile.background})
					default:
						if n > 4 || n > 2 && !nextHuman.Load() && !bytes.Contains(body, []byte("<task-notification>")) {
							t.Error("Unexpected actual parent request", n, string(body))
						}
						emitPlanFixture(w, 20, "", "ACTUAL_TASK_AGENT_PARENT_DONE")
					}
					return
				}
				switch children.Add(1) {
				case 1:
					var catalog []protocol.JSONObject
					_ = json.Unmarshal(mainObj(body).Get("tools"), &catalog)
					names := []string{}
					for _, tool := range catalog {
						names = append(names, tool.String("name"))
					}
					if !reflect.DeepEqual(names, original.Results[index].InitialTools) {
						t.Errorf("Actual initial child tool catalog differs from Source: Source%q Native%q", original.Results[index].InitialTools, names)
					}
					close(childEntered)
					if profile.promote {
						select {
						case <-childReleased:
						case <-request.Context().Done():
							return
						}
					}
					emitNativeTaskTool(w, "child-task-update", "TaskUpdate", map[string]any{"taskId": "99", "status": "in_progress"})
				case 2:
					var catalog []protocol.JSONObject
					_ = json.Unmarshal(mainObj(body).Get("tools"), &catalog)
					names := []string{}
					for _, tool := range catalog {
						names = append(names, tool.String("name"))
					}
					if !reflect.DeepEqual(names, original.Results[index].Tools) {
						t.Errorf("Actual post-tool child catalog differs from Source: Source%q Native%q", original.Results[index].Tools, names)
					}
					message := "Task not found"
					if len(original.Results[index].UnknownToolMessages) > 0 {
						message = "<tool_use_error>Error: No such tool available: TaskUpdate</tool_use_error>"
					}
					if !bytes.Contains(body, []byte(message)) {
						t.Errorf("Actual child TaskUpdate did not return its missing-task result: %s", body)
					}
					close(childUpdated)
					emitPlanFixture(w, 10, "", "ACTUAL_TASK_AGENT_CHILD_DONE")
				default:
					t.Errorf("Unexpected actual child model call %s", body)
				}
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) {
				options.Interactive = true
				options.Arguments.Tools = []string{"Agent,TaskUpdate"}
			})
			defer m.Close()
			defer release()
			if err := m.SetTerminalExpandedView(ctx, "none"); err != nil {
				t.Fatal(err)
			}
			turnDone := make(chan error, 1)
			go func() {
				turnDone <- m.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": "Run the genuine task Agent"})))
			}()
			if profile.promote {
				select {
				case <-childEntered:
				case <-ctx.Done():
					t.Fatal("Actual held foreground child", ctx.Err())
				}
				changed, err := m.BackgroundFromTerminal(ctx)
				if err != nil || !changed {
					t.Fatal("Actual foreground child promotion", changed, err)
				}
				release()
			}
			if err := <-turnDone; err != nil {
				t.Fatal(err)
			}
			select {
			case <-childUpdated:
			case <-ctx.Done():
				t.Fatal("Actual child TaskUpdate execution", ctx.Err())
			}
			view, err := m.TerminalExpandedView(ctx)
			global, readErr := m.Global.Get()
			want := original.Results[index]
			if err != nil || readErr != nil || view != want.ExpandedView || string(global.Get("showExpandedTodos")) != string(want.Global.Get("showExpandedTodos")) {
				t.Fatalf("Actual parent view differs from original child callback: Source%q/%s Native%q/%s errors%v/%v", want.ExpandedView, want.Global.Get("showExpandedTodos"), view, global.Get("showExpandedTodos"), err, readErr)
			}
			if children.Load() != 2 || parents.Load() < 2 {
				t.Fatal("Actual Agent/Harness task cardinality", parents.Load(), children.Load())
			}
			if err := m.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": "TASK_AGENT_NEXT_HUMAN"}))); err != nil || !nextHuman.Load() {
				t.Fatal("Next actual parent human after child task", err, nextHuman.Load())
			}
		})
	}
}

func TestNativeTerminalTaskViewRejectsForeignExpiredAndClosedScopes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Error("Scope guards invoked a model")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) { options.Interactive = true })
	defer m.Close()
	if err := m.SetTerminalExpandedView(ctx, "none"); err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.expandNativeTerminalTaskView(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("task expansion admitted a foreign actual Owner scope")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.expandNativeTerminalTaskView(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("Task callback admitted absent scope", err)
	}
	var expired *cooperative.Scope
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error { expired = scope; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.expandNativeTerminalTaskView(expired); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("Task callback admitted a retired actual Scope", err)
	}
	before, err := m.Global.Get()
	if err != nil || string(before.Get("showExpandedTodos")) != "false" {
		t.Fatal("Rejected callbacks changed the actual Global", err, string(rawMain(before)))
	}
	m.Close()
	if err := m.expandNativeTerminalTaskView(expired); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("Closed actual graph admitted task callback", err)
	}
	after, err := m.Global.Get()
	if err != nil || !bytes.Equal(rawMain(before), rawMain(after)) {
		t.Fatal("Closed graph changed Global after rejection", err, string(rawMain(after)))
	}
}
