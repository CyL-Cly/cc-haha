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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeTerminalTaskListActualToolsHeldModelPhysicalWatcherClearAndNextHuman(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(request.Body)
		n := calls.Add(1)
		if n > 1 && bytes.Contains(body, []byte(`"is_error":true`)) {
			t.Errorf("Actual task tool failed %s", body)
		}
		switch n {
		case 1:
			emitNativeTaskTool(w, "actual-task-create", "TaskCreate", map[string]any{"subject": "ACTUAL_SUBSCRIBED_TASK", "description": "same owner durable task"})
		case 2:
			close(entered)
			select {
			case <-release:
				emitNativeTaskTool(w, "actual-task-complete", "TaskUpdate", map[string]any{"taskId": "1", "status": "completed"})
			case <-request.Context().Done():
			}
		case 3:
			emitPlanFixture(w, 3, "", "ACTUAL_TASK_TURN_DONE")
		case 4:
			if !bytes.Contains(body, []byte("ACTUAL_NEXT_HUMAN")) || !bytes.Contains(body, []byte("actual-task-create")) || !bytes.Contains(body, []byte("actual-task-complete")) {
				t.Errorf("Next same-Harness task history %s", body)
			}
			emitPlanFixture(w, 4, "", "ACTUAL_NEXT_DONE")
		default:
			t.Errorf("Unexpected model request %d %s", n, body)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.Interactive = true; o.Arguments.Tools = []string{"TaskCreate,TaskUpdate"} })
	defer m.Close()
	changed := make(chan protocol.JSONObject, 64)
	initial, remove, err := m.SubscribeNativeTerminalTaskList(ctx, func(raw json.RawMessage) {
		value := mainObj(raw)
		select {
		case changed <- value:
		default:
			t.Error("Unbounded task-list publication")
		}
	})
	if err != nil || string(mainObj(initial).Get("tasks")) != "null" {
		t.Fatal("Actual initial singleton snapshot", string(initial), err)
	}
	defer remove()
	if err = m.SetTerminalExpandedView(ctx, "none"); err != nil {
		t.Fatal(err)
	}
	turnDone := make(chan error, 1)
	go func() {
		turnDone <- m.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": "Execute actual TaskCreate and TaskUpdate"})))
	}()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	wait := func(predicate func(protocol.JSONObject) bool) protocol.JSONObject {
		t.Helper()
		for {
			select {
			case value := <-changed:
				if predicate(value) {
					return value
				}
			case <-ctx.Done():
				t.Fatal("Actual native task-list publication", ctx.Err())
				return protocol.JSONObject{}
			}
		}
	}
	tasksOf := func(value protocol.JSONObject) []protocol.JSONObject {
		var values []protocol.JSONObject
		_ = json.Unmarshal(value.Get("tasks"), &values)
		return values
	}
	value := wait(func(value protocol.JSONObject) bool {
		items := tasksOf(value)
		return len(items) == 1 && items[0].String("status") == "pending"
	})
	if value.String("expandedView") != "tasks" || string(value.Get("expandedViewRevision")) != "1" {
		t.Fatal("Actual TaskCreate autoexpand was not projected", string(rawMain(value)))
	}
	global, e := m.Global.Get()
	if e != nil || string(global.Get("showExpandedTodos")) != "true" {
		t.Fatal("Actual source store callback global write", e, string(rawMain(global)))
	}
	select {
	case failure := <-turnDone:
		t.Fatal("Task-list read released/cancelled held model", failure)
	default:
	}
	list := value.String("listId")
	path := filepath.Join(m.taskStore.Directory(list), "1.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	physical := mainObj(data)
	physical.Set("status", quoteMain("in_progress"))
	physical.Set("metadata", []byte(`{"privateKeep":true}`))
	if err = os.WriteFile(path, rawMain(physical), 0600); err != nil {
		t.Fatal(err)
	}
	watchCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	for {
		select {
		case update := <-changed:
			items := tasksOf(update)
			if len(items) == 1 && items[0].String("status") == "in_progress" {
				goto observed
			}
		case <-watchCtx.Done():
			t.Fatal("Actual physical fs.watch update did not reach mounted list", watchCtx.Err())
		}
	}
observed:
	close(release)
	if err = <-turnDone; err != nil {
		t.Fatal(err)
	}
	completed := wait(func(value protocol.JSONObject) bool {
		items := tasksOf(value)
		return len(items) == 1 && items[0].String("status") == "completed"
	})
	if string(tasksOf(completed)[0].Get("metadata")) != `{"privateKeep":true}` {
		t.Fatal("TaskUpdate lost existing task fields", string(rawMain(completed)))
	}
	_ = wait(func(value protocol.JSONObject) bool { return string(value.Get("tasks")) == "null" })
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Five-second source hide did not reset actual task file", err)
	}
	if high, err := os.ReadFile(filepath.Join(m.taskStore.Directory(list), ".highwatermark")); err != nil || string(high) != "1" {
		t.Fatal("Actual durable reset lost high-water state", string(high), err)
	}
	if err = m.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": "ACTUAL_NEXT_HUMAN"}))); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatal("Task reads/reset called a model", calls.Load())
	}
	remove()
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		return m.Controller.SetTerminalExpandedViewOnOwner(scope, "none")
	}); err != nil {
		t.Fatal(err)
	}
	m.Close()
	select {
	case <-m.nativeTerminalTaskList.done:
	default:
		t.Fatal("Global close did not join actual task watcher/reader")
	}
}

func TestNativeTerminalTaskListHiddenCompletedFileRetainedUntilIncompleteTask(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var queries atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		queries.Add(1)
		t.Error("Read-only mounted TaskList called the model")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) { options.Interactive = true })
	defer m.Close()
	changed := make(chan protocol.JSONObject, 16)
	_, remove, err := m.SubscribeNativeTerminalTaskList(ctx, func(raw json.RawMessage) { changed <- mainObj(raw) })
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	wait := func() protocol.JSONObject {
		t.Helper()
		select {
		case value := <-changed:
			return value
		case <-ctx.Done():
			t.Fatal("Actual mounted task-list cache publication", ctx.Err())
			return protocol.JSONObject{}
		}
	}
	initial := wait()
	list := initial.String("listId")
	if list == "" || string(initial.Get("tasks")) != "null" {
		t.Fatal("Actual first empty list", string(rawMain(initial)))
	}
	data := protocol.NewJSONObject(fieldMain("subject", "Completed file while hidden"), fieldMain("description", ""), fieldMain("status", "completed"), fieldMain("blocks", []string{}), fieldMain("blockedBy", []string{}))
	completed, err := m.taskStore.Create(list, data)
	if err != nil {
		t.Fatal(err)
	}
	afterCompleted := wait()
	if string(afterCompleted.Get("tasks")) != "null" {
		t.Fatal("Source hidden cache was reopened by completed-only write", string(rawMain(afterCompleted)))
	}
	if _, err = os.Stat(filepath.Join(m.taskStore.Directory(list), completed.TaskID+".json")); err != nil {
		t.Fatal("Completed file must be preserved while Source list stays hidden", err)
	}
	data.Set("subject", quoteMain("Pending file restores visibility"))
	data.Set("status", quoteMain("pending"))
	if _, err = m.taskStore.Create(list, data); err != nil {
		t.Fatal(err)
	}
	visible := wait()
	var tasks []protocol.JSONObject
	if err = json.Unmarshal(visible.Get("tasks"), &tasks); err != nil || len(tasks) != 2 || tasks[0].String("status") != "completed" || tasks[1].String("status") != "pending" {
		t.Fatal("Original incomplete-task transition did not restore both durable tasks", err, string(rawMain(visible)))
	}
	if queries.Load() != 0 {
		t.Fatal("Tasks consumer issued model requests", queries.Load())
	}
}

func TestNativeTerminalTaskListFailedActualTaskUpdateAutoexpandsWithoutCreatingTask(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var queries atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(request.Body)
		switch queries.Add(1) {
		case 1:
			emitNativeTaskTool(w, "actual-missing-task-update", "TaskUpdate", map[string]any{"taskId": "99", "status": "in_progress"})
		case 2:
			if !bytes.Contains(body, []byte("Task not found")) {
				t.Errorf("Actual missing-task result did not return to same Harness: %s", body)
			}
			emitPlanFixture(w, 2, "", "ACTUAL_MISSING_TASK_DONE")
		default:
			t.Errorf("Task expansion invoked extra model: %s", body)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) {
		options.Interactive = true
		options.Arguments.Tools = []string{"TaskUpdate"}
	})
	defer m.Close()
	changed := make(chan protocol.JSONObject, 16)
	_, remove, err := m.SubscribeNativeTerminalTaskList(ctx, func(raw json.RawMessage) { changed <- mainObj(raw) })
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	select {
	case <-changed:
	case <-ctx.Done():
		t.Fatal("Actual empty-list mount", ctx.Err())
	}
	if err = m.SetTerminalExpandedView(ctx, "none"); err != nil {
		t.Fatal(err)
	}
	if err = m.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": "Try the actual missing-task update"}))); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case value := <-changed:
			if value.String("expandedView") != "tasks" {
				continue
			}
			if string(value.Get("tasks")) != "null" || string(value.Get("expandedViewRevision")) != "1" {
				t.Fatal("Failed Source TaskUpdate changes view but creates no task", string(rawMain(value)))
			}
			global, err := m.Global.Get()
			if err != nil || string(global.Get("showExpandedTodos")) != "true" || queries.Load() != 2 {
				t.Fatal("Actual failed-update AppState change was not persisted", err, string(rawMain(global)), queries.Load())
			}
			return
		case <-ctx.Done():
			t.Fatal("Actual failed-update expansion publication", ctx.Err())
		}
	}
}
