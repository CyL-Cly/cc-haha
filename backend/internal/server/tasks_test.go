package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

func TestDesktopTasksUseActualToolStoreAndResetPreservesIDs(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, ".claude")
	s, err := New(Options{HomeDir: root, ConfigDir: config, WorkDir: root, Factory: func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("task management must not start an Agent")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	store, err := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, status int) map[string]json.RawMessage {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = "127.0.0.1:32000"
		r.Host = "127.0.0.1:3000"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	if string(request("GET", "/api/tasks/lists", 200)["lists"]) != "[]" {
		t.Fatal("missing task directory should return an empty list")
	}
	input := protocol.NewJSONObject(protocol.JSONField{Name: "subject", Value: json.RawMessage(`"real task"`)}, protocol.JSONField{Name: "description", Value: json.RawMessage(`"task created by the same Task tool store"`)}, protocol.JSONField{Name: "status", Value: json.RawMessage(`"pending"`)}, protocol.JSONField{Name: "blocks", Value: json.RawMessage(`[]`)}, protocol.JSONField{Name: "blockedBy", Value: json.RawMessage(`[]`)})
	created, err := store.Create("desktop-list", input)
	if err != nil || created.TaskID != "1" {
		t.Fatalf("actual creation: %#v %v", created, err)
	}
	var task protocol.JSONObject
	if err := task.UnmarshalJSON(request("GET", "/api/tasks/lists/desktop-list/1", 200)["task"]); err != nil {
		t.Fatal(err)
	}
	if task.String("id") != "1" || task.String("subject") != "real task" || task.String("taskListId") != "desktop-list" {
		t.Fatalf("task view lost stored data: %s", request("GET", "/api/tasks", 200)["tasks"])
	}
	for _, path := range []string{"/api/tasks", "/api/tasks/lists/desktop-list"} {
		var tasks []protocol.JSONObject
		if err := json.Unmarshal(request("GET", path, 200)["tasks"], &tasks); err != nil || len(tasks) != 1 {
			t.Fatalf("%s: %#v %v", path, tasks, err)
		}
	}
	updated := protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: json.RawMessage(`"completed"`)})
	if _, err = store.Update("desktop-list", json.RawMessage(`"1"`), updated); err != nil {
		t.Fatal(err)
	}
	var lists []struct {
		ID                                                       string `json:"id"`
		TaskCount, CompletedCount, InProgressCount, PendingCount int
	}
	if err := json.Unmarshal(request("GET", "/api/tasks/lists", 200)["lists"], &lists); err != nil || len(lists) != 1 || lists[0].ID != "desktop-list" || lists[0].TaskCount != 1 || lists[0].CompletedCount != 1 || lists[0].PendingCount != 0 {
		t.Fatalf("actual task mutation did not reach summary: %#v %v", lists, err)
	}
	request("GET", "/api/tasks/lists/desktop-list/missing", http.StatusNotFound)
	request("PUT", "/api/tasks/lists/desktop-list", http.StatusMethodNotAllowed)
	request("POST", "/api/tasks", http.StatusMethodNotAllowed)
	if string(request("POST", "/api/tasks/lists/desktop-list/reset", 200)["ok"]) != "true" {
		t.Fatal("reset acknowledgement missing")
	}
	if string(request("GET", "/api/tasks/lists/desktop-list", 200)["tasks"]) != "[]" {
		t.Fatal("reset left tasks on disk")
	}
	created, err = store.Create("desktop-list", input)
	if err != nil || created.TaskID != "2" || created.Revision != 4 {
		t.Fatalf("desktop reset reused a Task tool ID or revision: %#v %v", created, err)
	}
}
