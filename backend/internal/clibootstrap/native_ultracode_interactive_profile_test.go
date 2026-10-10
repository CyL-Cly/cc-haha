package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// NonInteractive is one Source bootstrap state: Workflow authorization must
// not change without its Task/Todo selection and Notebook checkpoint consumer.
func TestActualSourceInteractiveMainPrintTaskTodoAndNotebookCheckpointProfile(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type profile struct {
		Interactive   bool           `json:"interactive"`
		Checkpointing bool           `json:"checkpointing"`
		Environment   map[string]any `json:"environment"`
	}
	rows := []profile{}
	for _, name := range []string{"interactive", "sdk", "sdk-checkpoint-tasks", "interactive-global-off", "interactive-env-off"} {
		r := profile{Interactive: true, Checkpointing: true, Environment: map[string]any{"CLAUDE_CODE_ENABLE_TASKS": nil, "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING": nil, "CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING": nil}}
		switch name {
		case "sdk":
			r.Interactive = false
		case "sdk-checkpoint-tasks":
			r.Interactive = false
			r.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
			r.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
		case "interactive-global-off":
			r.Checkpointing = false
		case "interactive-env-off":
			r.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"] = "1"
		}
		rows = append(rows, r)
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-interactive-profile.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INTERACTIVE_PROFILE_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if e := cmd.Run(); e != nil {
		t.Fatal(e, stdout.String(), stderr.String())
	}
	var source struct {
		Rows         []struct{ Task, Todo, History bool }
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, stdout.String(), stderr.String())
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			f.n.Environment["ANTHROPIC_API_KEY"] = "private-interactive-profile-fake"
			f.n.Environment["DISABLE_TELEMETRY"] = "1"
			f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			for key, value := range row.Environment {
				if value == nil {
					delete(f.n.Environment, key)
				} else {
					f.n.Environment[key] = value.(string)
				}
			}
			path := filepath.Join(f.n.CWD, "profile.ipynb")
			original := `{"nbformat":4,"nbformat_minor":5,"metadata":{},"cells":[{"id":"c","cell_type":"code","metadata":{},"outputs":[],"execution_count":null,"source":["original"]}]}`
			f.write(t, path, original)
			calls := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				calls++
				if bytes.Contains(body, []byte(`"is_error":true`)) {
					t.Errorf("actual tools failed %.2500s", body)
				}
				if calls == 1 {
					var request struct{ Tools []protocol.JSONObject }
					_ = json.Unmarshal(body, &request)
					present := map[string]bool{}
					for _, tool := range request.Tools {
						present[tool.String("name")] = true
					}
					if present["TaskCreate"] != source.Rows[i].Task || present["TodoWrite"] != source.Rows[i].Todo {
						t.Errorf("Source catalog task/todo %v want %+v", present, source.Rows[i])
					}
					emitNativeTaskTool(w, "profile-read-before", "Read", map[string]string{"file_path": path})
				} else if calls == 2 {
					if source.Rows[i].Task {
						emitNativeTaskTool(w, "profile-task", "TaskCreate", map[string]string{"subject": "private profile task", "description": "actual shared state"})
					} else {
						emitNativeTaskTool(w, "profile-todo", "TodoWrite", map[string]any{"todos": []map[string]string{{"content": "private profile todo", "activeForm": "checking profile", "status": "pending"}}})
					}
				} else if calls == 3 {
					emitNativeTaskTool(w, "profile-notebook", "NotebookEdit", map[string]string{"notebook_path": path, "cell_id": "c", "new_source": "PHYSICAL_NOTEBOOK_PROFILE"})
				} else if calls == 4 {
					emitNativeTaskTool(w, "profile-read-after", "Read", map[string]string{"file_path": path})
				} else {
					emitPlanFixture(w, calls, "", "PHYSICAL_PROFILE_DONE")
				}
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			disabled := false
			m, e := NewMainPrint(MainPrintOptions{Interactive: row.Interactive, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private interactive profile", Tools: []string{"Read", "TaskCreate", "TodoWrite", "NotebookEdit"}, MaxTurns: 8, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: bytes.NewReader(nil), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			defer m.ShutdownSession(context.Background())
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if e = m.StartSession(ctx); e != nil {
				t.Fatal(e)
			}
			if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					// Source saveGlobalConfig requires a new identity to write.
					next := current.Clone()
					next.Set("fileCheckpointingEnabled", rawMain(row.Checkpointing))
					return &next, nil
				})
			}); e != nil {
				t.Fatal(e)
			}
			if e = m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Run actual task/todo and notebook edit"})})); e != nil {
				t.Fatal(e)
			}
			if calls != 5 {
				t.Errorf("actual model calls %d", calls)
			}
			data, e := os.ReadFile(path)
			if e != nil || !bytes.Contains(data, []byte("PHYSICAL_NOTEBOOK_PROFILE")) {
				t.Fatal("actual notebook write missing", e, string(data))
			}
			if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				state := m.Controller.AppState()
				var snapshots []protocol.JSONObject
				_ = json.Unmarshal(state.FileHistory.Get("snapshots"), &snapshots)
				if (len(snapshots) > 0) != source.Rows[i].History {
					return fmt.Errorf("actual Source history enabled=%v, snapshots=%s", source.Rows[i].History, rawMain(snapshots))
				}
				if source.Rows[i].History {
					backups, _ := snapshots[0].Object("trackedFileBackups")
					backup, ok := backups.Object("profile.ipynb")
					if !ok || backup.String("backupFileName") == "" {
						return fmt.Errorf("actual Notebook original backup absent %s", rawMain(snapshots))
					}
					data, e := os.ReadFile(filepath.Join(f.config, "file-history", m.Controller.SessionID(), backup.String("backupFileName")))
					if e != nil || string(data) != original {
						return fmt.Errorf("actual original checkpoint content changed %v %s", e, data)
					}
				}
				if source.Rows[i].Task && state.ExpandedView != "tasks" {
					return fmt.Errorf("actual Task AppState view %q", state.ExpandedView)
				}
				if source.Rows[i].Todo && len(state.Todos.OwnProperties()) == 0 {
					return fmt.Errorf("actual Todo AppState absent")
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
