package workflow

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type reconstructed struct {
	data   protocol.JSONObject
	agents []protocol.JSONObject
}

func nullish(raw json.RawMessage) bool { return raw == nil || string(raw) == "null" }
func defaultRaw(raw, fallback json.RawMessage) json.RawMessage {
	if nullish(raw) {
		return fallback
	}
	return raw
}
func (h *History) collectAgents(dir string, runs map[string]*reconstructed, modified map[string]float64, fallback string) error {
	entries, e := h.options.Files.ReadDir(dir)
	if e != nil {
		return nil
	}
	fallbackIndex := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, e := h.options.Files.ReadFile(path)
		if e != nil || !json.Valid(raw) {
			continue
		}
		stat, e := h.options.Files.Stat(path)
		if e != nil {
			continue
		}
		if string(raw) == "null" {
			return &APIError{500, "An unexpected error occurred", "INTERNAL_ERROR"}
		}
		meta := jsonObject(raw)
		provenance := jsonObject(meta.Get("workflow"))
		runID := provenance.String("runId")
		if nullish(provenance.Get("runId")) {
			runID = fallback
		}
		run := runs[runID]
		if runID == "" || run == nil {
			continue
		}
		if nonempty(meta, "ownerAgentId") != "" && nullish(run.data.Get("ownerAgentId")) {
			run.data.Set("ownerAgentId", meta.Get("ownerAgentId"))
		}
		id := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "agent-"), ".meta.json")
		present := false
		for _, agent := range run.agents {
			present = present || agent.String("agentId") == id
		}
		if present {
			continue
		}
		fallbackIndex++
		index := defaultRaw(provenance.Get("agentIndex"), valueRaw(fallbackIndex))
		label := meta.Get("description")
		if nullish(label) {
			label = quoted("agent " + resultText(index))
		}
		agent := protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: quoted(id)}, protocol.JSONField{Name: "label", Value: label}, protocol.JSONField{Name: "phaseIndex", Value: defaultRaw(provenance.Get("phaseIndex"), []byte("0"))})
		if title := nonempty(provenance, "phaseTitle"); title != "" {
			agent.Set("phaseTitle", provenance.Get("phaseTitle"))
		}
		agent.Set("agentIndex", index)
		agent.Set("state", quoted("start"))
		if !nullish(meta.Get("workflow")) {
			key := runID + ":" + resultText(index)
			oldIndex := -1
			for i, old := range run.agents {
				if string(old.Get("agentIndex")) == string(index) {
					oldIndex = i
					break
				}
			}
			mtime := float64(stat.ModTime().UnixNano()) / 1e6
			if oldIndex >= 0 {
				if previous, ok := modified[key]; ok && mtime <= previous {
					continue
				}
				run.agents[oldIndex] = agent
				modified[key] = mtime
				continue
			}
			modified[key] = mtime
		}
		run.agents = append(run.agents, agent)
	}
	return nil
}
func (h *History) journalState(ctx context.Context, id, runID string) (map[string]bool, map[string]bool, error) {
	done, started := map[string]bool{}, map[string]bool{}
	dirs, e := h.sessionDirs(ctx, &id)
	if e != nil {
		return nil, nil, e
	}
	for _, dir := range dirs {
		raw, e := h.options.Files.ReadFile(filepath.Join(dir.path, "subagents", "workflows", runID, "journal.jsonl"))
		if e != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			entry := jsonObject([]byte(line))
			id := entry.String("agentId")
			if id == "" {
				continue
			}
			if entry.String("type") == "result" {
				done[id] = true
			}
			if entry.String("type") == "started" {
				started[id] = true
			}
		}
	}
	return done, started, nil
}
func expectedOutput(path, root, id, task string) bool {
	relative, e := filepath.Rel(root, path)
	return e == nil && relative != "." && relative != "" && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) && filepath.Base(task) == task && filepath.Base(path) == task+".output" && filepath.Base(filepath.Dir(path)) == "tasks" && filepath.Base(filepath.Dir(filepath.Dir(path))) == id
}
func (h *History) taskOutput(id, runID string, notice protocol.JSONObject) []protocol.JSONObject {
	path := notice.String("outputFile")
	if path == "" {
		return []protocol.JSONObject{}
	}
	candidate, e := filepath.Abs(path)
	if e != nil || !expectedOutput(candidate, h.options.TempDir, id, notice.String("taskId")) {
		return []protocol.JSONObject{}
	}
	real, e := h.options.Files.Realpath(candidate)
	if e != nil || !expectedOutput(real, h.options.TempDir, id, notice.String("taskId")) {
		return []protocol.JSONObject{}
	}
	stat, e := h.options.Files.Lstat(candidate)
	if e != nil || !stat.Mode().IsRegular() || stat.Mode()&osSymlink != 0 {
		return []protocol.JSONObject{}
	}
	file, e := h.options.Files.OpenNoFollow(candidate)
	if e != nil {
		return []protocol.JSONObject{}
	}
	defer file.Close()
	stat, e = file.Stat()
	if e != nil || !stat.Mode().IsRegular() || stat.Size() > 8*1024*1024 {
		return []protocol.JSONObject{}
	}
	raw, e := readLimited(file, 8*1024*1024)
	if e != nil {
		return []protocol.JSONObject{}
	}
	data := jsonObject(raw)
	if data.String("workflowRunId") != runID {
		return []protocol.JSONObject{}
	}
	return sanitizeProgress(data.Get("workflowProgress"))
}
func safeInteger(raw json.RawMessage) bool {
	var v float64
	return json.Unmarshal(raw, &v) == nil && string(raw) != "null" && v >= 0 && v <= 9007199254740991 && math.Floor(v) == v
}
func sanitizeProgress(raw json.RawMessage) []protocol.JSONObject {
	var rows []protocol.JSONObject
	if json.Unmarshal(raw, &rows) != nil {
		return []protocol.JSONObject{}
	}
	if len(rows) > 500 {
		rows = rows[len(rows)-500:]
	}
	out := []protocol.JSONObject{}
	positions := map[string]int{}
	for _, row := range rows {
		if !safeInteger(row.Get("index")) {
			continue
		}
		kind := row.String("type")
		event := protocol.NewJSONObject()
		if kind == "workflow_phase" {
			if _, e := protocol.DecodeJavaScriptJSONString(row.Get("title")); e != nil {
				continue
			}
			event.Set("type", row.Get("type"))
			event.Set("index", row.Get("index"))
			event.Set("title", row.Get("title"))
			if k := row.String("kind"); k == "meta" || k == "script" {
				event.Set("kind", row.Get("kind"))
			}
		} else if kind == "workflow_agent" {
			if _, e := protocol.DecodeJavaScriptJSONString(row.Get("label")); e != nil {
				continue
			}
			state := row.String("state")
			if state != "start" && state != "progress" && state != "done" && state != "error" {
				continue
			}
			for _, key := range []string{"type", "index", "label", "state"} {
				event.Set(key, row.Get(key))
			}
			for _, key := range []string{"phaseTitle", "agentType", "model", "agentId", "error", "promptPreview", "resultPreview", "lastToolName"} {
				if _, e := protocol.DecodeJavaScriptJSONString(row.Get(key)); e == nil {
					event.Set(key, row.Get(key))
				}
			}
			for _, key := range []string{"phaseIndex", "queuedAt", "startedAt", "lastProgressAt", "durationMs", "tokens", "toolCalls"} {
				if safeInteger(row.Get(key)) {
					event.Set(key, row.Get(key))
				}
			}
			for _, key := range []string{"cached", "skipped", "blocked"} {
				if string(row.Get(key)) == "true" || string(row.Get(key)) == "false" {
					event.Set(key, row.Get(key))
				}
			}
			if value := row.String("isolation"); value == "worktree" || value == "remote" {
				event.Set("isolation", row.Get("isolation"))
			}
		} else {
			continue
		}
		key := kind + ":" + resultText(row.Get("index"))
		if position, exists := positions[key]; exists {
			out[position] = event
		} else {
			positions[key] = len(out)
			out = append(out, event)
		}
	}
	return out
}
func (h *History) progress(ctx context.Context, id string, runs map[string]*reconstructed, launches map[string]launch) (map[string][]protocol.JSONObject, error) {
	if h.options.Notifications == nil {
		return nil, &CapabilityError{"same server SessionTaskNotifications consumer"}
	}
	notifications, e := h.options.Notifications(ctx, id)
	if e != nil {
		return map[string][]protocol.JSONObject{}, nil
	}
	type candidate struct {
		notice protocol.JSONObject
		order  int
	}
	byRun := map[string][]candidate{}
	for i, notice := range notifications {
		runID := notice.String("workflowRunId")
		if runID == "" || runs[runID] == nil {
			continue
		}
		if launch, ok := launches[runID]; ok {
			at, valid := timestamp(launch.timestamp)
			end, _ := timestamp(notice.String("timestamp"))
			if notice.String("taskId") != launch.taskID || notice.String("ownerAgentId") != launch.ownerID || launch.toolUseID != "" && notice.String("toolUseId") != launch.toolUseID || valid && end != 0 && end < at {
				continue
			}
		}
		byRun[runID] = append(byRun[runID], candidate{notice, i})
	}
	result := map[string][]protocol.JSONObject{}
	for runID, candidates := range byRun {
		sort.SliceStable(candidates, func(i, j int) bool {
			a, _ := timestamp(candidates[i].notice.String("timestamp"))
			b, _ := timestamp(candidates[j].notice.String("timestamp"))
			if a != b {
				return a > b
			}
			return candidates[i].order > candidates[j].order
		})
		events := h.taskOutput(id, runID, candidates[0].notice)
		hasAgent := false
		for _, event := range events {
			hasAgent = hasAgent || event.String("type") == "workflow_agent"
		}
		if hasAgent {
			result[runID] = events
		}
	}
	return result, nil
}
func (s *Service) ReconstructSessionRuns(ctx context.Context, id string) ([]protocol.JSONObject, error) {
	if e := s.checkHistory(ctx); e != nil {
		return nil, e
	}
	h := s.history
	existing, e := h.listRuns(ctx, &id, nil)
	if e != nil {
		return nil, e
	}
	dirs, e := h.sessionDirs(ctx, &id)
	if e != nil {
		return nil, e
	}
	life, e := h.lifecycle(ctx, id, dirs)
	if e != nil {
		return nil, e
	}
	byRun := map[string]*reconstructed{}
	order := []string{}
	for _, old := range existing {
		runID := old.String("runId")
		status := old.String("status")
		if status == "unknown" {
			status = "running"
		}
		data := protocol.NewJSONObject(protocol.JSONField{Name: "runId", Value: quoted(runID)}, protocol.JSONField{Name: "taskId", Value: quoted(runID)}, protocol.JSONField{Name: "workflowName", Value: old.Get("workflowName")}, protocol.JSONField{Name: "status", Value: quoted(status)}, protocol.JSONField{Name: "startedAt", Value: old.Get("startedAt")}, protocol.JSONField{Name: "updatedAt", Value: old.Get("startedAt")})
		if byRun[runID] == nil {
			order = append(order, runID)
		}
		byRun[runID] = &reconstructed{data, []protocol.JSONObject{}}
	}
	for _, runID := range life.launchOrder {
		if byRun[runID] != nil {
			continue
		}
		launched := life.launches[runID]
		at, _ := timestamp(launched.timestamp)
		data := protocol.NewJSONObject(protocol.JSONField{Name: "runId", Value: quoted(runID)}, protocol.JSONField{Name: "taskId", Value: quoted(launched.taskID)})
		if launched.ownerID != "" {
			data.Set("ownerAgentId", quoted(launched.ownerID))
		}
		data.Set("workflowName", quoted(launched.name))
		data.Set("status", quoted("running"))
		data.Set("startedAt", valueRaw(at))
		data.Set("updatedAt", valueRaw(at))
		order = append(order, runID)
		byRun[runID] = &reconstructed{data, []protocol.JSONObject{}}
	}
	if len(byRun) == 0 {
		return []protocol.JSONObject{}, nil
	}
	modified := map[string]float64{}
	for _, dir := range dirs {
		root := filepath.Join(dir.path, "subagents")
		if e := h.collectAgents(root, byRun, modified, ""); e != nil {
			return nil, e
		}
		legacy := filepath.Join(root, "workflows")
		entries, e := h.options.Files.ReadDir(legacy)
		if e != nil {
			continue
		}
		for _, entry := range entries {
			if byRun[entry.Name()] != nil {
				if e := h.collectAgents(filepath.Join(legacy, entry.Name()), byRun, modified, entry.Name()); e != nil {
					return nil, e
				}
			}
		}
	}
	for runID, run := range byRun {
		launched, ok := life.launches[runID]
		at, valid := timestamp(launched.timestamp)
		if !ok || !valid {
			continue
		}
		first := math.Inf(1)
		for _, agent := range run.agents {
			index := jsonNumber(agent.Get("agentIndex"))
			if mtime, ok := modified[runID+":"+resultText(agent.Get("agentIndex"))]; ok && mtime >= at && index < first {
				first = index
			}
		}
		if math.IsInf(first, 1) {
			continue
		}
		filtered := []protocol.JSONObject{}
		for _, agent := range run.agents {
			mtime, ok := modified[runID+":"+resultText(agent.Get("agentIndex"))]
			if jsonNumber(agent.Get("agentIndex")) < first || ok && mtime >= at {
				filtered = append(filtered, agent)
			}
		}
		run.agents = filtered
	}
	progress, e := h.progress(ctx, id, byRun, life.launches)
	if e != nil {
		return nil, e
	}
	for runID, events := range progress {
		byRun[runID].data.Set("progress", valueRaw(events))
	}
	result := []protocol.JSONObject{}
	for _, runID := range order {
		run := byRun[runID]
		launched, ok := life.launches[runID]
		if ok {
			run.data.Set("taskId", quoted(launched.taskID))
			if launched.ownerID != "" {
				run.data.Set("ownerAgentId", quoted(launched.ownerID))
			}
			if at, valid := timestamp(launched.timestamp); valid {
				run.data.Set("startedAt", valueRaw(at))
				run.data.Set("updatedAt", valueRaw(at))
			}
			if end := findTerminal(life.terminals, launched, life.launches); end != nil {
				run.data.Set("status", quoted(end.status))
				if at, valid := timestamp(end.timestamp); valid {
					run.data.Set("updatedAt", valueRaw(at))
					run.data.Set("endedAt", valueRaw(at))
				}
				if end.result != "" {
					run.data.Set("result", quoted(end.result))
				}
				if end.status == "failed" || end.status == "stopped" {
					if end.summary != "" {
						run.data.Set("error", quoted(end.summary))
					}
				}
			}
		}
		done, started, e := h.journalState(ctx, id, runID)
		if e != nil {
			return nil, e
		}
		status := run.data.String("status")
		for i := range run.agents {
			agent := &run.agents[i]
			if done[agent.String("agentId")] {
				agent.Set("state", quoted("done"))
			} else if status == "running" {
				state := "start"
				if started[agent.String("agentId")] {
					state = "progress"
				}
				agent.Set("state", quoted(state))
			} else if status == "completed" {
				agent.Set("state", quoted("done"))
				agent.Set("skipped", []byte("true"))
			} else {
				agent.Set("state", quoted("error"))
				agent.Set("error", run.data.Get("error"))
				if status == "stopped" {
					agent.Set("skipped", []byte("true"))
				}
			}
		}
		sort.SliceStable(run.agents, func(i, j int) bool {
			return jsonNumber(run.agents[i].Get("agentIndex")) < jsonNumber(run.agents[j].Get("agentIndex"))
		})
		run.data.Set("agents", valueRaw(run.agents))
		result = append(result, run.data)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return jsonNumber(result[i].Get("startedAt")) > jsonNumber(result[j].Get("startedAt"))
	})
	return result, nil
}
func (s *Service) GetAgentRunState(ctx context.Context, id, agentID string) (json.RawMessage, error) {
	runs, e := s.ReconstructSessionRuns(ctx, id)
	if e != nil {
		return nil, e
	}
	for _, run := range runs {
		var agents []protocol.JSONObject
		_ = json.Unmarshal(run.Get("agents"), &agents)
		for _, agent := range agents {
			if agent.String("agentId") == agentID {
				return valueRaw(protocol.NewJSONObject(protocol.JSONField{Name: "run", Value: valueRaw(run)}, protocol.JSONField{Name: "agent", Value: valueRaw(agent)})), nil
			}
		}
	}
	return []byte("null"), nil
}
