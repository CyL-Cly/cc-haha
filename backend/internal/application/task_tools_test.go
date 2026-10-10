package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestNativeTaskToolsUseActualStoreAndSharedAppState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var calls atomic.Int32
	var path string
	var desktopTasks http.Handler
	var taskList string
	tools := []struct {
		name  string
		input any
	}{{"TaskCreate", map[string]any{"subject": "Actual task", "description": "Persist with the Go task store", "activeForm": "Creating actual task"}}, {"TaskUpdate", map[string]any{"taskId": "1", "status": "in_progress", "metadata": map[string]any{"native": true}}}, {"TaskGet", map[string]any{"taskId": "1"}}, {"TaskList", map[string]any{}}, {"TaskUpdate", map[string]any{"taskId": "1", "status": "completed"}}}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinal := int(calls.Add(1))
		body, _ := io.ReadAll(r.Body)
		if ordinal > 1 {
			if !bytes.Contains(body, []byte(`"type":"tool_result"`)) || bytes.Contains(body, []byte(`"is_error":true`)) {
				t.Errorf("actual Task round failed: %s", body)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("model advanced before task commit: %v", err)
			} else if ordinal == 2 {
				var task protocol.JSONObject
				if err := task.UnmarshalJSON(data); err != nil || task.String("status") != "pending" {
					t.Errorf("missing original pending state: %s error=%v", data, err)
				}
			}
			request := httptest.NewRequest(http.MethodGet, "/api/tasks/lists/"+taskList, nil)
			request.RemoteAddr = "127.0.0.1:32000"
			request.Host = "127.0.0.1:3000"
			view := httptest.NewRecorder()
			desktopTasks.ServeHTTP(view, request)
			var payload struct {
				Tasks []protocol.JSONObject `json:"tasks"`
			}
			if err := json.Unmarshal(view.Body.Bytes(), &payload); err != nil || view.Code != 200 || len(payload.Tasks) != 1 || payload.Tasks[0].String("subject") != "Actual task" {
				t.Errorf("desktop API cannot read actual Agent Task commits: %d %s %v", view.Code, view.Body.String(), err)
			} else if ordinal == len(tools)+1 && payload.Tasks[0].String("status") != "completed" {
				t.Errorf("desktop API missed the final Agent Task state: %s", view.Body.String())
			}
		}
		if ordinal <= len(tools) {
			tool := tools[ordinal-1]
			response(w, "task-"+strconv.Itoa(ordinal), tool.name, tool.input, "")
		} else {
			response(w, "task-final", "", nil, "TASKS COMPLETE")
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = []string{"TaskCreate,TaskUpdate,TaskGet,TaskList"}
	o.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "true"
	o.Environment["CLAUDE_CODE_TASK_LIST_ID"] = "shared tasks😀"
	managed := o.Native.ManagedRoot
	o.Native = nil
	o.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}
	path = filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "tasks", taskstore.SanitizePathComponent(o.Environment["CLAUDE_CODE_TASK_LIST_ID"]), "1.json")
	taskList = taskstore.SanitizePathComponent(o.Environment["CLAUDE_CODE_TASK_LIST_ID"])
	desktop, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: o.NativeFactory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = desktop.Close(context.Background()) })
	desktopTasks = desktop
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(ctx, tui.Input{Text: "Execute the real Task tools."}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 6 {
		t.Fatal("Task tools did not complete through actual model loop", calls.Load())
	}
	data, err := os.ReadFile(path)
	var task, metadata protocol.JSONObject
	if err == nil {
		err = task.UnmarshalJSON(data)
	}
	if err == nil {
		err = metadata.UnmarshalJSON(task.Get("metadata"))
	}
	if err != nil || task.String("status") != "completed" || string(metadata.Get("native")) != "true" {
		t.Fatalf("actual task mutation %s %v", data, err)
	}
	err = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		view, e := s.process.Controller.ExpandedViewOnOwner(scope)
		if e != nil {
			return e
		}
		if view != "tasks" {
			t.Errorf("Task tools did not expand actual shared AppState: %q", view)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
