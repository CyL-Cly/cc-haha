package teamreader

import (
	"errors"
	"math"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type transcriptCreation struct {
	position           int
	name               string
	started, completed float64
}

func (s *Service) transcriptCreations(ordered []protocol.JSONObject, results map[string]projectedResult) []transcriptCreation {
	out := []transcriptCreation{}
	for position, message := range ordered {
		if message.String("type") != "tool_use" {
			continue
		}
		for _, tool := range rows(message.Get("content")) {
			if tool.String("type") != "tool_use" || tool.String("name") != "TeamCreate" || !nonblank(tool.Get("id")) {
				continue
			}
			result, ok := results[tool.String("id")]
			if !successful(result, ok) {
				continue
			}
			input := obj(tool.Get("input"))
			name := obj(raw(map[string]any{"v": stringFallback(result.value.Get("team_name"), input.Get("team_name"))})).String("v")
			started := s.parsedDate(message.String("timestamp"))
			completed := started
			if result.timestamp != "" {
				completed = s.parsedDate(result.timestamp)
			}
			if name != "" && finite(started) && finite(completed) {
				out = append(out, transcriptCreation{position, name, started, completed})
			}
		}
	}
	return out
}
func creationDistance(value transcriptCreation, created float64) float64 {
	lower, upper := min(value.started, value.completed), max(value.started, value.completed)
	if created < lower {
		return lower - created
	}
	if created > upper {
		return created - upper
	}
	return 0
}
func lifecycleEnd(creation transcriptCreation, ordered []protocol.JSONObject, results map[string]projectedResult, creations []transcriptCreation) int {
	end := len(ordered)
	for _, next := range creations {
		if next.position > creation.position {
			end = next.position
			break
		}
	}
	for position := creation.position + 1; position < end; position++ {
		message := ordered[position]
		if message.String("type") != "tool_use" {
			continue
		}
		for _, tool := range rows(message.Get("content")) {
			if tool.String("type") != "tool_use" || tool.String("name") != "TeamDelete" || !nonblank(tool.Get("id")) {
				continue
			}
			result, ok := results[tool.String("id")]
			input := obj(tool.Get("input"))
			name := obj(raw(map[string]any{"v": stringFallback(input.Get("team_name"), result.value.Get("team_name"))})).String("v")
			if successful(result, ok) && (name == "" || name == creation.name) {
				return position + 1
			}
		}
	}
	return end
}
func cloneArchiveTask(task protocol.JSONObject) (protocol.JSONObject, error) {
	for _, key := range []string{"blocks", "blockedBy"} {
		if value := task.Get(key); len(value) == 0 || value[0] != '[' {
			return protocol.JSONObject{}, errors.New("Archived task " + key + " is not iterable")
		}
	}
	return task.Clone(), nil
}

type replayEvent struct {
	name, id string
	input    protocol.JSONObject
	result   projectedResult
	at       float64
	revision *float64
	order    int
}

func revision(value []byte) *float64 {
	if !safeInteger(value) || numeric(value) < 0 {
		return nil
	}
	n := numeric(value)
	return &n
}
func (s *Service) ReconcileArchivedTasks(snapshot protocol.JSONObject, messages []protocol.JSONObject) (*protocol.JSONObject, error) {
	generated := s.parsedDate(snapshot.String("generatedAt"))
	team := obj(snapshot.Get("team"))
	created := numeric(team.Get("createdAt"))
	if !finite(generated) || !finite(created) {
		return nil, nil
	}
	snapshotRevision := revision(snapshot.Get("taskListRevision"))
	results := resultRecords(messages)
	ordered := s.orderedMessages(messages)
	creations := s.transcriptCreations(ordered, results)
	candidates := []transcriptCreation{}
	for _, value := range creations {
		if value.name == team.String("name") && creationDistance(value, created) <= 5000 {
			candidates = append(candidates, value)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		difference := creationDistance(candidates[i], created) - creationDistance(candidates[j], created)
		if difference != 0 {
			return difference < 0
		}
		return math.Abs(candidates[i].started-created) < math.Abs(candidates[j].started-created)
	})
	if len(candidates) == 0 {
		return nil, nil
	}
	creation := candidates[0]
	end := lifecycleEnd(creation, ordered, results, creations)
	tasks := newOrderedTasks()
	for _, task := range rows(snapshot.Get("tasks")) {
		copy, err := cloneArchiveTask(task)
		if err != nil {
			return nil, err
		}
		tasks.set(task.String("id"), copy)
	}
	events := []replayEvent{}
	for position := creation.position; position < end; position++ {
		message := ordered[position]
		if message.String("type") != "tool_use" {
			continue
		}
		for blockIndex, tool := range rows(message.Get("content")) {
			if tool.String("type") != "tool_use" || !nonblank(tool.Get("id")) || !nonblank(tool.Get("name")) {
				continue
			}
			id, name := tool.String("id"), tool.String("name")
			result, ok := results[id]
			if !successful(result, ok) {
				continue
			}
			input := obj(tool.Get("input"))
			started, completed := s.parsedDate(message.String("timestamp")), s.parsedDate(result.timestamp)
			markerKey, revisionKey := "taskListMutationAt", "taskListMutationRevision"
			if name == "TaskList" || name == "TeamDelete" {
				markerKey, revisionKey = "taskListSnapshotAt", "taskListSnapshotRevision"
			}
			causalAt := s.parsedDate(result.value.String(markerKey))
			rev := revision(result.value.Get(revisionKey))
			if snapshotRevision != nil && rev != nil {
				if *rev < *snapshotRevision || *rev == *snapshotRevision && name != "TeamDelete" {
					continue
				}
			} else if finite(causalAt) {
				if causalAt <= generated {
					continue
				}
			} else if !finite(started) || !finite(completed) || started <= generated || completed <= generated {
				continue
			}
			at := completed
			if finite(causalAt) {
				at = causalAt
			}
			events = append(events, replayEvent{name, id, input, result, at, rev, position*1000 + blockIndex})
		}
	}
	allRevision := true
	for _, event := range events {
		if event.revision == nil {
			allRevision = false
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		difference := a.at - b.at
		if allRevision {
			difference = *a.revision - *b.revision
		}
		if difference != 0 {
			return difference < 0
		}
		return a.order < b.order
	})
	applied := snapshotRevision
	changed := false
	terminal := ""
	for _, event := range events {
		if event.revision != nil {
			v := *event.revision
			if applied != nil {
				v = max(v, *applied)
			}
			applied = &v
		}
		switch event.name {
		case "TaskCreate":
			resultTask := obj(event.result.value.Get("task"))
			if !nonblank(resultTask.Get("id")) {
				continue
			}
			id := resultTask.String("id")
			task := applyTaskUpdate(tasks.ensure(id, team.String("name")), event.input)
			for _, key := range []string{"subject", "description"} {
				if nonblank(resultTask.Get(key)) {
					task.Set(key, resultTask.Get(key))
				}
			}
			tasks.set(id, task)
			changed = true
		case "TaskUpdate":
			if !nonblank(event.input.Get("taskId")) {
				continue
			}
			id := event.input.String("taskId")
			if event.input.String("status") == "deleted" {
				changed = tasks.delete(id) || changed
			} else {
				tasks.set(id, applyTaskUpdate(tasks.ensure(id, team.String("name")), event.input))
				changed = true
			}
		case "TaskList", "TeamDelete":
			key := "tasks"
			if event.name == "TeamDelete" {
				key = "finalTasks"
			}
			if value := event.result.value.Get(key); len(value) > 0 && value[0] == '[' {
				replaceTaskList(tasks, value, team.String("name"), event.name == "TaskList")
				if event.name == "TeamDelete" {
					terminal = event.id
				}
				changed = true
			}
		}
	}
	if !changed {
		return nil, nil
	}
	finalizeTaskEdges(tasks)
	all := tasks.all()
	sortTasks(all)
	out := protocol.NewJSONObject(field("tasks", all))
	if applied != nil {
		out.Set("taskListRevision", raw(*applied))
	}
	if terminal != "" {
		out.Set("terminalTaskFrameId", raw(terminal))
	}
	return &out, nil
}
func terminalLifecycleTasks(snapshot, lifecycle protocol.JSONObject) (*protocol.JSONObject, error) {
	team := obj(snapshot.Get("team"))
	terminals := rows(lifecycle.Get("terminals"))
	for i := len(terminals) - 1; i >= 0; i-- {
		receipt := terminals[i]
		identity := obj(receipt.Get("identity"))
		if string(identity.Get("teamName")) != string(team.Get("name")) || string(identity.Get("createdAt")) != string(team.Get("createdAt")) || string(identity.Get("leadSessionId")) != string(team.Get("leadSessionId")) {
			continue
		}
		tasks := []protocol.JSONObject{}
		for _, task := range rows(receipt.Get("tasks")) {
			if jsTruthy(obj(task.Get("metadata")).Get("_internal")) {
				continue
			}
			copy, err := cloneArchiveTask(task)
			if err != nil {
				return nil, err
			}
			copy.Set("taskListId", team.Get("name"))
			tasks = append(tasks, copy)
		}
		sortTasks(tasks)
		out := protocol.NewJSONObject(field("tasks", tasks), protocol.JSONField{Name: "taskListRevision", Value: receipt.Get("revision")}, protocol.JSONField{Name: "terminalTaskFrameId", Value: receipt.Get("frameId")})
		return &out, nil
	}
	return nil, nil
}
func preferTerminalTaskFrame(transcript, lifecycle *protocol.JSONObject) *protocol.JSONObject {
	if lifecycle == nil {
		return transcript
	}
	if transcript == nil {
		return lifecycle
	}
	if transcript.Get("taskListRevision") != nil && lifecycle.Get("taskListRevision") != nil && numeric(transcript.Get("taskListRevision")) > numeric(lifecycle.Get("taskListRevision")) {
		return transcript
	}
	copy := lifecycle.Clone()
	id := transcript.Get("terminalTaskFrameId")
	if id == nil || string(id) == "null" {
		id = lifecycle.Get("terminalTaskFrameId")
	}
	copy.Set("terminalTaskFrameId", id)
	return &copy
}
