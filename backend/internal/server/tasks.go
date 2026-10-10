package server

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

func (server *Server) tasksHTTP(writer http.ResponseWriter, request *http.Request, parts []string) error {
	store, err := taskstore.New(taskstore.Options{ConfigDir: server.options.ConfigDir, PID: os.Getpid(), Now: time.Now})
	if err != nil {
		return err
	}
	if len(parts) > 2 && parts[2] == "lists" {
		list, id := "", ""
		if len(parts) > 3 {
			list = parts[3]
		}
		if len(parts) > 4 {
			id = parts[4]
		}
		if request.Method == http.MethodPost && list != "" && id == "reset" {
			if err = store.Reset(list); err != nil {
				return err
			}
			writeJSON(writer, 200, map[string]bool{"ok": true})
			return nil
		}
		if request.Method != http.MethodGet {
			return &apiError{405, "METHOD_NOT_ALLOWED", "Method " + request.Method + " not allowed on /api/tasks/lists"}
		}
		if list == "" {
			lists, err := store.DesktopTaskLists()
			if err == nil {
				writeJSON(writer, 200, map[string]any{"lists": lists})
			}
			return err
		}
		tasks, err := store.DesktopTasksForList(list)
		if err != nil {
			return err
		}
		if id != "" {
			for _, task := range tasks {
				var taskID string
				_ = json.Unmarshal(task.Get("id"), &taskID)
				if taskID == id {
					writeJSON(writer, 200, map[string]any{"task": task})
					return nil
				}
			}
			return notFound("Task not found: " + list + "/" + id)
		}
		writeJSON(writer, 200, map[string]any{"tasks": tasks})
		return nil
	}
	if request.Method != http.MethodGet {
		return &apiError{405, "METHOD_NOT_ALLOWED", "Method " + request.Method + " not allowed on /api/tasks"}
	}
	tasks, err := store.DesktopAllTasks()
	if err == nil {
		writeJSON(writer, 200, map[string]any{"tasks": tasks})
	}
	return err
}
