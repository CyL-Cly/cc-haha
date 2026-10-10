package teamreader

import (
	"encoding/json"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type orderedTasks struct {
	values map[string]protocol.JSONObject
	ids    []string
}

func newOrderedTasks() *orderedTasks {
	return &orderedTasks{values: map[string]protocol.JSONObject{}, ids: []string{}}
}
func (t *orderedTasks) set(id string, value protocol.JSONObject) {
	if _, ok := t.values[id]; !ok {
		t.ids = append(t.ids, id)
	}
	t.values[id] = value
}
func (t *orderedTasks) delete(id string) bool {
	if _, ok := t.values[id]; !ok {
		return false
	}
	delete(t.values, id)
	for i, key := range t.ids {
		if key == id {
			t.ids = append(t.ids[:i], t.ids[i+1:]...)
			break
		}
	}
	return true
}
func (t *orderedTasks) all() []protocol.JSONObject {
	out := []protocol.JSONObject{}
	for _, id := range t.ids {
		out = append(out, t.values[id].Clone())
	}
	return out
}
func (t *orderedTasks) ensure(id, team string) protocol.JSONObject {
	value, ok := t.values[id]
	if !ok {
		value = protocol.NewJSONObject(field("id", id), field("subject", "Task #"+id), field("description", ""), field("status", "pending"), field("blocks", []string{}), field("blockedBy", []string{}), field("taskListId", team))
		t.set(id, value)
	}
	return value
}
func edgeStrings(value json.RawMessage) []json.RawMessage {
	var array []json.RawMessage
	_ = json.Unmarshal(value, &array)
	out := []json.RawMessage{}
	for _, v := range array {
		if nonemptyRaw(v) {
			out = append(out, v)
		}
	}
	return out
}
func addUniqueEdges(a, b []json.RawMessage) []json.RawMessage {
	out := []json.RawMessage{}
	seen := map[string]bool{}
	for _, list := range [][]json.RawMessage{a, b} {
		for _, value := range list {
			key := string(value)
			if !seen[key] {
				seen[key] = true
				out = append(out, value)
			}
		}
	}
	return out
}
func applyTaskUpdate(task, input protocol.JSONObject) protocol.JSONObject {
	for _, key := range []string{"subject", "description", "activeForm", "owner"} {
		if nonblank(input.Get(key)) {
			task.Set(key, input.Get(key))
		}
	}
	if status := input.String("status"); status == "pending" || status == "in_progress" || status == "completed" {
		task.Set("status", input.Get("status"))
	}
	task.Set("blocks", raw(addUniqueEdges(edgeStrings(task.Get("blocks")), edgeStrings(input.Get("addBlocks")))))
	task.Set("blockedBy", raw(addUniqueEdges(edgeStrings(task.Get("blockedBy")), edgeStrings(input.Get("addBlockedBy")))))
	if metadata := obj(input.Get("metadata")); metadata.IsObject() {
		current := obj(task.Get("metadata"))
		if !current.IsObject() {
			current = protocol.NewJSONObject()
		}
		current.Assign(metadata)
		task.Set("metadata", raw(current))
	}
	return task
}
func replaceTaskList(tasks *orderedTasks, values json.RawMessage, team string, reverseOnly bool) {
	records := []protocol.JSONObject{}
	for _, record := range rows(values) {
		if nonblank(record.Get("id")) {
			records = append(records, record)
		}
	}
	rebuild := reverseOnly
	for _, record := range records {
		if len(record.Get("blockedBy")) == 0 || record.Get("blockedBy")[0] != '[' {
			rebuild = false
		}
	}
	listed := map[string]bool{}
	for _, record := range records {
		id := record.String("id")
		listed[id] = true
		task := tasks.ensure(id, team)
		if status := record.String("status"); status == "pending" || status == "in_progress" || status == "completed" {
			task.Set("status", record.Get("status"))
		}
		if nonblank(record.Get("subject")) {
			task.Set("subject", record.Get("subject"))
		}
		if stringRaw(record.Get("description")) {
			task.Set("description", record.Get("description"))
		}
		for _, key := range []string{"activeForm", "owner"} {
			if nonblank(record.Get(key)) {
				task.Set(key, record.Get(key))
			} else if key == "owner" {
				task.Delete(key)
			}
		}
		if rebuild {
			task.Set("blocks", raw([]string{}))
			task.Set("blockedBy", raw(edgeStrings(record.Get("blockedBy"))))
		} else {
			for _, key := range []string{"blocks", "blockedBy"} {
				if v := record.Get(key); len(v) > 0 && v[0] == '[' {
					task.Set(key, raw(edgeStrings(v)))
				}
			}
		}
		if metadata := obj(record.Get("metadata")); metadata.IsObject() {
			task.Set("metadata", raw(metadata))
		}
		tasks.set(id, task)
	}
	for _, id := range append([]string(nil), tasks.ids...) {
		if !listed[id] {
			tasks.delete(id)
		}
	}
}
func finalizeTaskEdges(tasks *orderedTasks) {
	for _, id := range tasks.ids {
		task := tasks.values[id]
		for _, blockedID := range edgeStrings(task.Get("blocks")) {
			var target string
			_ = json.Unmarshal(blockedID, &target)
			if blocked, ok := tasks.values[target]; ok {
				blocked.Set("blockedBy", raw(addUniqueEdges(edgeStrings(blocked.Get("blockedBy")), []json.RawMessage{task.Get("id")})))
				tasks.values[target] = blocked
			}
		}
		for _, blockerID := range edgeStrings(task.Get("blockedBy")) {
			var target string
			_ = json.Unmarshal(blockerID, &target)
			if blocker, ok := tasks.values[target]; ok {
				blocker.Set("blocks", raw(addUniqueEdges(edgeStrings(blocker.Get("blocks")), []json.RawMessage{task.Get("id")})))
				tasks.values[target] = blocker
			}
		}
	}
}

type projectedResult struct {
	value     protocol.JSONObject
	isError   bool
	timestamp string
}

func resultRecords(messages []protocol.JSONObject) map[string]projectedResult {
	results := map[string]projectedResult{}
	for _, message := range messages {
		if message.String("type") != "tool_result" {
			continue
		}
		for _, block := range rows(message.Get("content")) {
			if !nonblank(block.Get("tool_use_id")) {
				continue
			}
			id := block.String("tool_use_id")
			value := obj(message.Get("toolUseResult"))
			if !value.IsObject() {
				value = block
			}
			previous := results[id]
			timestamp := message.String("timestamp")
			if timestamp == "" {
				timestamp = previous.timestamp
			}
			results[id] = projectedResult{value: value, isError: previous.isError || string(block.Get("is_error")) == "true", timestamp: timestamp}
		}
	}
	return results
}
func successful(result projectedResult, exists bool) bool {
	return exists && !result.isError && string(result.value.Get("success")) != "false"
}
func (s *Service) orderedMessages(messages []protocol.JSONObject) []protocol.JSONObject {
	out := append([]protocol.JSONObject(nil), messages...)
	sort.SliceStable(out, func(i, j int) bool {
		difference := s.parsedDate(out[i].String("timestamp")) - s.parsedDate(out[j].String("timestamp"))
		return finite(difference) && difference < 0
	})
	return out
}
func stringFallback(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if nonblank(value) {
			return value
		}
	}
	return nil
}
