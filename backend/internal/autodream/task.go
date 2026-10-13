package autodream

import (
	"encoding/json"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Task projections retain raw JS strings, including lone UTF-16 code units,
// through the original task/UI boundary. The caller owns registration/abort.
func taskPathKey(path json.RawMessage) string {
	units, _ := protocol.DecodeJavaScriptJSONString(path)
	return string(protocol.QuoteJavaScriptUTF16(units))
}

func RegisterTask(id, outputFile string, reviewing int, prior, now float64) protocol.JSONObject {
	quote := func(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "id", Value: quote(id)}, protocol.JSONField{Name: "type", Value: json.RawMessage(`"dream"`)},
		protocol.JSONField{Name: "status", Value: json.RawMessage(`"running"`)}, protocol.JSONField{Name: "description", Value: json.RawMessage(`"dreaming"`)},
		protocol.JSONField{Name: "startTime", Value: json.RawMessage(strconv.FormatFloat(now, 'f', -1, 64))}, protocol.JSONField{Name: "outputFile", Value: quote(outputFile)},
		protocol.JSONField{Name: "outputOffset", Value: json.RawMessage(`0`)}, protocol.JSONField{Name: "notified", Value: json.RawMessage(`false`)},
		protocol.JSONField{Name: "phase", Value: json.RawMessage(`"starting"`)}, protocol.JSONField{Name: "sessionsReviewing", Value: json.RawMessage(strconv.Itoa(reviewing))},
		protocol.JSONField{Name: "filesTouched", Value: json.RawMessage(`[]`)}, protocol.JSONField{Name: "turns", Value: json.RawMessage(`[]`)},
		protocol.JSONField{Name: "priorMtime", Value: json.RawMessage(strconv.FormatFloat(prior, 'f', -1, 64))},
	)
}
func IsDreamTask(task protocol.JSONObject) bool { return task.String("type") == "dream" }
func AddTurn(task protocol.JSONObject, turn protocol.JSONObject, touchedPaths []json.RawMessage) (protocol.JSONObject, bool) {
	var files, turns []json.RawMessage
	_ = json.Unmarshal(task.Get("filesTouched"), &files)
	_ = json.Unmarshal(task.Get("turns"), &turns)
	seen := map[string]bool{}
	for _, path := range files {
		seen[taskPathKey(path)] = true
	}
	additions := []json.RawMessage{}
	for _, path := range touchedPaths {
		key := taskPathKey(path)
		if !seen[key] {
			seen[key] = true
			additions = append(additions, path)
		}
	}
	if turn.String("text") == "" && string(turn.Get("toolUseCount")) == "0" && len(additions) == 0 {
		return task, false
	}
	next := task.Clone()
	if len(additions) > 0 {
		next.Set("phase", json.RawMessage(`"updating"`))
		files = append(files, additions...)
		raw, _ := json.Marshal(files)
		next.Set("filesTouched", raw)
	}
	if len(turns) > 29 {
		turns = turns[len(turns)-29:]
	}
	raw, _ := json.Marshal(turn)
	turns = append(turns, raw)
	raw, _ = json.Marshal(turns)
	next.Set("turns", raw)
	return next, true
}

type TerminalEvent struct{ Status, Summary string }

func FinishTask(task protocol.JSONObject, status string, now float64) (protocol.JSONObject, *TerminalEvent) {
	if !IsDreamTask(task) {
		return task, nil
	}
	next := task.Clone()
	raw, _ := json.Marshal(status)
	next.Set("status", raw)
	next.Set("endTime", json.RawMessage(strconv.FormatFloat(now, 'f', -1, 64)))
	next.Set("notified", json.RawMessage(`true`))
	next.Delete("abortController")
	summary := "Auto-dream failed"
	if status == "completed" {
		summary = "Auto-dream completed"
	}
	return next, &TerminalEvent{status, summary}
}
func KillTask(task protocol.JSONObject, now float64) (protocol.JSONObject, *TerminalEvent, *float64) {
	if !IsDreamTask(task) || task.String("status") != "running" {
		return task, nil, nil
	}
	next, event := FinishTask(task, "killed", now)
	event.Status = "stopped"
	event.Summary = "Auto-dream stopped"
	prior, _ := strconv.ParseFloat(string(task.Get("priorMtime")), 64)
	return next, event, &prior
}
