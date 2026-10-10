package workflow

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TaskRecord holds the serializable fields of the original LocalWorkflowTask.
// Actual cancellation, joining and agent controllers are owned by the one
// Controller.BackgroundTask entry that registers this record.
func TaskRecord(params protocol.JSONObject, now float64, outputPath string) protocol.JSONObject {
	description := defaultRaw(params.Get("summary"), quoted("Dynamic workflow"))
	record := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: params.Get("taskId")}, protocol.JSONField{Name: "type", Value: quoted("local_workflow")}, protocol.JSONField{Name: "status", Value: quoted("running")}, protocol.JSONField{Name: "description", Value: description}, protocol.JSONField{Name: "toolUseId", Value: params.Get("toolUseId")}, protocol.JSONField{Name: "startTime", Value: defaultRaw(params.Get("startTime"), valueRaw(now))}, protocol.JSONField{Name: "outputFile", Value: quoted(outputPath)}, protocol.JSONField{Name: "outputOffset", Value: []byte("0")}, protocol.JSONField{Name: "notified", Value: []byte("false")})
	for _, key := range []string{"script", "scriptPath", "args"} {
		record.Set(key, params.Get(key))
	}
	record.Set("prompt", params.Get("script"))
	for _, key := range []string{"summary", "workflowName", "title", "phases", "defaultModel", "workflowRunId", "ownerAgentId"} {
		record.Set(key, params.Get(key))
	}
	record.Set("workflowProgress", []byte("[]"))
	for _, key := range []string{"progressVersion", "agentCount", "totalTokens", "totalToolCalls"} {
		record.Set(key, []byte("0"))
	}
	record.Set("logs", []byte("[]"))
	return record
}

// ApplyProgressBatch is the original keyed row reducer. It preserves unknown
// task fields and log ordering; it does not allocate a second task registry.
func ApplyProgressBatch(task protocol.JSONObject, events []protocol.JSONObject) (protocol.JSONObject, error) {
	if len(events) == 0 || task.String("status") != "running" {
		return task, nil
	}
	var rows []protocol.JSONObject
	if e := json.Unmarshal(task.Get("workflowProgress"), &rows); e != nil {
		return protocol.JSONObject{}, &CapabilityError{"Workflow task progress array"}
	}
	positions := map[string]int{}
	for i, row := range rows {
		kind := row.String("type")
		if kind == "workflow_phase" || kind == "workflow_agent" {
			positions[kind+":"+resultText(row.Get("index"))] = i
		}
	}
	count := jsonNumber(task.Get("agentCount"))
	logs := false
	for _, event := range events {
		if event.String("type") == "workflow_log" {
			rows = append(rows, event.Clone())
			logs = true
			continue
		}
		key := event.String("type") + ":" + resultText(event.Get("index"))
		if i, ok := positions[key]; ok {
			rows[i] = event.Clone()
		} else {
			positions[key] = len(rows)
			rows = append(rows, event.Clone())
		}
		if event.String("type") == "workflow_agent" {
			count = math.Max(count, jsonNumber(event.Get("index")))
		}
	}
	if logs && len(rows) > 1000 {
		remaining := len(rows) - 500
		kept := []protocol.JSONObject{}
		for _, row := range rows {
			if remaining > 0 && row.String("type") == "workflow_log" {
				remaining--
				continue
			}
			kept = append(kept, row)
		}
		rows = kept
	}
	tokens, tools := 0.0, 0.0
	for _, row := range rows {
		if row.String("type") != "workflow_agent" {
			continue
		}
		for _, key := range []string{"tokens", "toolCalls"} {
			raw := row.Get(key)
			if nullish(raw) {
				continue
			}
			var value float64
			if e := json.Unmarshal(raw, &value); e != nil {
				return protocol.JSONObject{}, &CapabilityError{"non-numeric original Workflow progress counter"}
			}
			if key == "tokens" {
				tokens += value
			} else {
				tools += value
			}
		}
	}
	next := task.Clone()
	next.Set("workflowProgress", valueRaw(rows))
	next.Set("progressVersion", valueRaw(jsonNumber(task.Get("progressVersion"))+float64(len(events))))
	next.Set("agentCount", valueRaw(count))
	next.Set("totalTokens", valueRaw(tokens))
	next.Set("totalToolCalls", valueRaw(tools))
	return next, nil
}
func ResumePrompt(task protocol.JSONObject) string {
	args := ""
	if raw := task.Get("args"); raw != nil {
		serialized, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
		args = ", args: " + string(serialized)
	}
	return "Resume the paused workflow by calling: Workflow({scriptPath: '" + task.String("scriptPath") + "', resumeFromRunId: '" + task.String("workflowRunId") + "'" + args + "}) — completed agents return cached results."
}
func Notification(params protocol.JSONObject, outputPath string) (string, error) {
	status := params.String("status")
	summary := params.String("summary")
	headline := "Dynamic workflow \"" + summary + "\" was stopped"
	if status == "completed" {
		headline = "Dynamic workflow \"" + summary + "\" completed"
	} else if status == "failed" {
		cause := params.String("error")
		if cause == "" {
			cause = "Unknown error"
		}
		headline = "Dynamic workflow \"" + summary + "\" failed: " + cause
	}
	args := ""
	if raw := params.Get("args"); raw != nil {
		serialized, e := protocol.ParseAndStringifyJavaScriptJSON(raw)
		if e != nil {
			return "", e
		}
		args = ", args: " + string(serialized)
	}
	script, runID, dir := params.String("scriptPath"), params.String("workflowRunId"), params.String("transcriptDir")
	resume := ""
	if script != "" && runID != "" {
		resume = "Workflow({scriptPath: '" + script + "', resumeFromRunId: '" + runID + "'" + args + "})"
	}
	sections := []string{}
	if status != "completed" {
		recovery := []string{}
		if resume != "" {
			recovery = append(recovery, "To resume after editing the script, call: "+resume)
		}
		if dir != "" {
			recovery = append(recovery, "Agent transcripts: "+dir)
		}
		if len(recovery) > 0 {
			sections = append(sections, "\n<recovery>\n"+strings.Join(recovery, "\n")+"\n</recovery>")
		}
	} else if dir != "" {
		notes := []string{"Per-agent results: " + dir + "/journal.jsonl — one {\"type\":\"result\",...} line per completed agent with its full return value.", "If the result above is empty or unexpected, Read this file BEFORE diagnosing — do not assume agents returned non-empty results."}
		if resume != "" {
			notes = append(notes, "To re-run with edited post-processing: "+resume+" — agents whose (prompt, opts) are unchanged replay from cache.")
		}
		sections = append(sections, "\n<transcripts>\n"+strings.Join(notes, "\n")+"\n</transcripts>")
	}
	var failures []string
	if e := json.Unmarshal(params.Get("failures"), &failures); params.Get("failures") != nil && e != nil {
		return "", &CapabilityError{"Workflow failure strings"}
	}
	if len(failures) > 0 {
		sections = append(sections, "\n<failures>\n"+strings.Join(failures, "\n")+"\n</failures>")
	}
	resultSection := ""
	if raw := params.Get("result"); raw != nil {
		resultSection = "\n<result>" + resultText(raw) + "</result>"
	}
	toolID := ""
	if id := params.String("toolUseId"); id != "" {
		toolID = "\n<tool-use-id>" + id + "</tool-use-id>"
	}
	number := func(key string) string {
		raw := params.Get(key)
		if raw == nil {
			return "undefined"
		}
		text, e := protocol.JavaScriptStringJSON(raw)
		if e != nil {
			return "undefined"
		}
		var value string
		_ = json.Unmarshal(text, &value)
		return value
	}
	return fmt.Sprintf("<task-notification>\n<task-id>%s</task-id>%s\n<task-type>local_workflow</task-type>\n<workflow-run-id>%s</workflow-run-id>\n<output-file>%s</output-file>\n<status>%s</status>\n<summary>%s</summary>%s%s\n<usage><agent_count>%s</agent_count><subagent_tokens>%s</subagent_tokens><tool_uses>%s</tool_uses><duration_ms>%s</duration_ms></usage>\n</task-notification>", params.String("taskId"), toolID, runID, outputPath, status, headline, resultSection, strings.Join(sections, ""), number("agentCount"), number("totalTokens"), number("totalToolCalls"), number("durationMs")), nil
}
