package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// The desktop reads the same files as Task tools, but Source TaskService has
// a deliberately more permissive legacy projection than the tool schema.
// Reading through Get/Snapshot would discard those old tasks or expose the
// tool's internal status mapping instead of the original desktop response.
func desktopReadDir(path string) ([]os.DirEntry, error) {
	dir, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	// Bun's original fs.readdir retains native enumeration order. os.ReadDir
	// sorts names, changing list order and tie order in the stable task sort.
	return dir.ReadDir(-1)
}

func (s *Service) DesktopTasksForList(list string) ([]protocol.JSONObject, error) {
	dir := filepath.Join(s.options.ConfigDir, "tasks", list)
	files, err := desktopReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []protocol.JSONObject{}, nil
	}
	if err != nil {
		return nil, err
	}
	tasks := []protocol.JSONObject{}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		value, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			continue
		}
		if task := desktopTask(value, list); task != nil {
			tasks = append(tasks, *task)
		}
	}
	comparator := collate.New(language.English)
	sort.SliceStable(tasks, func(a, b int) bool {
		left, right := tasks[a].String("id"), tasks[b].String("id")
		x, y := decimalPrefix(left), decimalPrefix(right)
		if !math.IsNaN(x) && !math.IsNaN(y) {
			return x < y
		}
		return comparator.CompareString(left, right) < 0
	})
	return tasks, nil
}

func desktopTruthy(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || string(value) == "null" || string(value) == "false" {
		return false
	}
	switch value[0] {
	case '"':
		units, err := protocol.DecodeJavaScriptJSONString(value)
		return err == nil && len(units) != 0
	case '[', '{', 't':
		return true
	default:
		n, err := strconv.ParseFloat(string(value), 64)
		return (err == nil || math.IsInf(n, 0)) && n != 0
	}
}

func desktopTask(value json.RawMessage, list string) *protocol.JSONObject {
	var data protocol.JSONObject
	if data.UnmarshalJSON(value) != nil || !data.IsObject() || !desktopTruthy(data.Get("id")) || !desktopTruthy(data.Get("subject")) {
		return nil
	}
	var metadata protocol.JSONObject
	if metadata.UnmarshalJSON(data.Get("metadata")) == nil && desktopTruthy(metadata.Get("_internal")) {
		return nil
	}
	id, err := protocol.JavaScriptStringJSON(data.Get("id"))
	if err != nil {
		return nil
	}
	description := data.Get("description")
	if !desktopTruthy(description) {
		description = quote("")
	}
	status := data.String("status")
	if status != "pending" && status != "in_progress" && status != "completed" {
		status = "pending"
	}
	array := func(name string) json.RawMessage {
		value := bytes.TrimSpace(data.Get(name))
		if len(value) == 0 || value[0] != '[' {
			return emptyArray()
		}
		return value
	}
	task := protocol.NewJSONObject(
		protocol.JSONField{Name: "id", Value: id},
		protocol.JSONField{Name: "subject", Value: data.Get("subject")},
		protocol.JSONField{Name: "description", Value: description},
		protocol.JSONField{Name: "activeForm", Value: data.Get("activeForm")},
		protocol.JSONField{Name: "owner", Value: data.Get("owner")},
		protocol.JSONField{Name: "status", Value: quote(status)},
		protocol.JSONField{Name: "blocks", Value: array("blocks")},
		protocol.JSONField{Name: "blockedBy", Value: array("blockedBy")},
		protocol.JSONField{Name: "metadata", Value: data.Get("metadata")},
		protocol.JSONField{Name: "taskListId", Value: quote(list)},
	)
	return &task
}

func (s *Service) DesktopTaskLists() ([]protocol.JSONObject, error) {
	files, err := desktopReadDir(filepath.Join(s.options.ConfigDir, "tasks"))
	if errors.Is(err, os.ErrNotExist) {
		return []protocol.JSONObject{}, nil
	}
	if err != nil {
		return nil, err
	}
	lists := []protocol.JSONObject{}
	for _, file := range files {
		if !file.IsDir() {
			continue
		}
		tasks, err := s.DesktopTasksForList(file.Name())
		if err != nil {
			return nil, err
		}
		if len(tasks) == 0 {
			continue
		}
		counts := map[string]int{}
		for _, task := range tasks {
			counts[task.String("status")]++
		}
		lists = append(lists, protocol.NewJSONObject(
			protocol.JSONField{Name: "id", Value: quote(file.Name())},
			protocol.JSONField{Name: "taskCount", Value: raw(len(tasks))},
			protocol.JSONField{Name: "completedCount", Value: raw(counts["completed"])},
			protocol.JSONField{Name: "inProgressCount", Value: raw(counts["in_progress"])},
			protocol.JSONField{Name: "pendingCount", Value: raw(counts["pending"])},
		))
	}
	return lists, nil
}

func (s *Service) DesktopAllTasks() ([]protocol.JSONObject, error) {
	lists, err := s.DesktopTaskLists()
	if err != nil {
		return nil, err
	}
	tasks := []protocol.JSONObject{}
	for _, list := range lists {
		current, err := s.DesktopTasksForList(list.String("id"))
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, current...)
	}
	return tasks, nil
}

// Reset uses the same lifecycle and durable list leases as Create/Update.
// It removes only task JSON files and retains revision/high-water state, so
// the next TaskCreate in another process cannot reuse a deleted task's ID.
func (s *Service) Reset(list string) error {
	return s.withLifecycle(list, true, func(lifecycle) error {
		return s.withList(list, func() error {
			highest := s.highestID(list)
			if highest > s.highWaterMark(list) {
				if err := os.WriteFile(filepath.Join(s.tasksDir(list), ".highwatermark"), []byte(numberText(highest)), 0666); err != nil {
					return err
				}
			}
			files, err := os.ReadDir(s.tasksDir(list))
			if err != nil {
				files = nil // Source reset treats a failed directory read as empty.
			}
			targets := []string{}
			for _, file := range files {
				if strings.HasSuffix(file.Name(), ".json") && !strings.HasPrefix(file.Name(), ".") {
					targets = append(targets, filepath.Join(s.tasksDir(list), file.Name()))
				}
			}
			if len(targets) > 0 {
				if _, err := s.bumpRevision(list); err != nil {
					return err
				}
			}
			for _, target := range targets {
				_ = os.Remove(target) // Source ignores individual unlink failures.
			}
			s.notify()
			return nil
		})
	})
}
