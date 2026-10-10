package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

type resolvedAgentRef struct {
	resolution       *subagentResolution
	lookupRef, owner string
	complete         bool
}

func (server *Server) resolveAgentRef(ctx context.Context, info sessionInfo, parent subagentTranscript, ref string) (*resolvedAgentRef, error) {
	exact := resolveSubagentMessages(parent.messages, ref)
	nested := canonicalNestedAgentRef(ref)
	unknown := func() *subagentResolution { return &subagentResolution{} }
	if nested == nil {
		if exact == nil && parent.historyComplete {
			return nil, nil
		}
		if exact == nil {
			exact = unknown()
		}
		return &resolvedAgentRef{exact, ref, "", parent.historyComplete}, nil
	}
	if strict := agentIDHint(nested.parent); strict != "" {
		if exact != nil {
			return &resolvedAgentRef{exact, nested.leaf, strict, parent.historyComplete}, nil
		}
		transcript, err := server.readSubagentTranscript(ctx, info, strict, nested.leaf)
		if err != nil {
			return nil, err
		}
		resolution := resolveSubagentMessages(transcript.messages, nested.leaf)
		if resolution == nil && transcript.historyComplete && parent.historyComplete {
			return nil, nil
		}
		if resolution == nil {
			resolution = unknown()
		}
		return &resolvedAgentRef{resolution, nested.leaf, strict, parent.historyComplete && transcript.historyComplete}, nil
	}
	separator := strings.LastIndex(nested.parent, "@")
	if separator <= 0 || separator == len(nested.parent)-1 {
		return nil, nil
	}
	fragments, err := server.subagentFragments(ctx, info, nested.parent[:separator], nested.leaf)
	if err != nil {
		return nil, err
	}
	complete := parent.historyComplete
	for _, fragment := range fragments {
		complete = complete && fragment.historyComplete
	}
	for i := len(fragments) - 1; i >= 0; i-- {
		if resolution := resolveSubagentMessages(fragments[i].messages, nested.leaf); resolution != nil {
			return &resolvedAgentRef{resolution, nested.leaf, fragments[i].agentID, complete}, nil
		}
	}
	if !complete {
		return &resolvedAgentRef{unknown(), nested.leaf, nested.parent, false}, nil
	}
	return nil, nil
}
func subagentStatus(resolution *subagentResolution, notification historyObject) string {
	if resolution.isError {
		return "failed"
	}
	if value := stringField(notification, "status"); value != "" {
		return value
	}
	if resolution.hasResult && !resolution.async {
		return "completed"
	}
	return "running"
}
func (server *Server) subagentRunByTool(ctx context.Context, info sessionInfo, ref, taskHint string) (map[string]any, error) {
	parent, err := server.subagentLookup(ctx, info, ref, "")
	if err != nil {
		return nil, err
	}
	resolved, err := server.resolveAgentRef(ctx, info, parent, ref)
	if err != nil || resolved == nil {
		return nil, err
	}
	resolution := resolved.resolution
	notification := notificationForAgent(parent.notifications, resolved.lookupRef, resolved.owner)
	safeTask := agentIDHint(taskHint)
	teammateName := ""
	if at := strings.Index(resolution.agentID, "@"); at >= 0 {
		teammateName = resolution.agentID[:at]
	}
	fragments := []subagentFragment{}
	if teammateName != "" {
		fragments, err = server.subagentFragments(ctx, info, teammateName, "")
		if err != nil {
			return nil, err
		}
	}
	metadataID, err := server.metadataAgentID(ctx, info, resolved.lookupRef, resolved.owner)
	if err != nil {
		return nil, err
	}
	metadataID = agentIDHint(metadataID)
	transcript := emptySubagentTranscript()
	if len(fragments) > 0 {
		transcript = mergeSubagentFragments(fragments)
	} else {
		transcript, err = server.resolveSubagentTranscript(ctx, info, metadataID, resolution.agentID, safeTask, stringField(notification, "taskId"))
		if err != nil {
			return nil, err
		}
	}
	activity := transcript
	if len(fragments) > 0 {
		activity = subagentActivityFragments(fragments)
	} else if transcript.agentID != "" {
		activity = projectSubagentActivity(transcript, false)
	}
	complete := resolved.complete && transcript.historyComplete
	status := subagentStatus(resolution, notification)
	if !resolved.complete && !resolution.hasResult && stringField(notification, "status") == "" {
		status = "unknown"
	}
	id := transcript.agentID
	if id == "" {
		id = metadataID
	}
	if id == "" {
		id = agentIDHint(resolution.agentID)
	}
	var agent any
	if id != "" {
		agent = id
	}
	messages, truncated := truncateSubagentMessages(activity.messages)
	response := map[string]any{
		"sessionId": info.ID, "toolUseId": ref, "agentId": agent, "status": status,
		"messages": messages, "taskNotifications": transcript.notifications, "activityTaskNotifications": activity.notifications,
		"truncated": truncated || !complete, "historyComplete": complete, "activityComplete": complete,
		"canSendMessage": id != "" && (teammateName != "" || resolution.async && status == "running"),
		"source":         "session-history",
	}
	if truncated {
		response["activityMessages"] = activity.messages
	}
	if len(transcript.messages) > 0 {
		response["source"] = "subagent-jsonl"
	} else if safeTask != "" {
		response["source"] = "live-task"
	}
	if task := stringField(notification, "taskId"); task != "" {
		response["taskId"] = task
	} else if safeTask != "" {
		response["taskId"] = safeTask
	}
	for key, value := range map[string]string{"description": resolution.description, "prompt": resolution.prompt, "summary": stringField(notification, "summary"), "outputFile": stringField(notification, "outputFile")} {
		if value != "" {
			response[key] = value
		}
	}
	result := stringField(notification, "result")
	if result == "" {
		result = resolution.result
	}
	if result != "" {
		response["result"] = result
	}
	var usage map[string]float64
	if transcript.historyComplete {
		usage = subagentTranscriptUsage(transcript.messages)
	}
	usage = mergeSubagentUsage(resolution.usage, usage)
	if usage != nil {
		response["usage"] = subagentUsageJSON(usage)
	}
	timestamps := []string{}
	for _, raw := range transcript.messages {
		timestamps = append(timestamps, stringField(historyRecord(raw), "timestamp"))
	}
	for _, notice := range transcript.notifications {
		timestamps = append(timestamps, stringField(notice, "timestamp"))
	}
	if latest := latestSubagentTimestamp(resolution.updatedAt, stringField(notification, "timestamp"), latestSubagentTimestamp(timestamps...)); latest != "" {
		response["updatedAt"] = latest
	}
	return response, nil
}
func (server *Server) subagentRunByAgent(ctx context.Context, info sessionInfo, id string) (map[string]any, error) {
	id = agentIDHint(id)
	if id == "" {
		return nil, nil
	}
	transcript, err := server.resolveSubagentTranscript(ctx, info, id)
	if err != nil || transcript.agentID == "" {
		return nil, err
	}
	if server.options.WorkflowAgentRunState == nil {
		return nil, unsupported("subagent by-agent workflow reconstruction")
	}
	// Source obtains this state from durable workflow reconstruction, not metadata.
	state, err := server.options.WorkflowAgentRunState(ctx, info.ID, transcript.agentID)
	if err != nil {
		state = nil
	}
	workflow := historyRecord(state)
	status := "completed"
	if workflow != nil {
		agent := historyRecord(workflow["agent"])
		run := historyRecord(workflow["run"])
		if stringField(agent, "state") == "done" {
			status = "completed"
		} else if stringField(run, "status") == "stopped" {
			status = "stopped"
		} else if stringField(agent, "state") == "error" || stringField(run, "status") == "failed" {
			status = "failed"
		} else {
			status = "running"
		}
	} else if !transcript.historyComplete {
		status = "unknown"
	}
	activity := projectSubagentActivity(transcript, false)
	messages, truncated := truncateSubagentMessages(activity.messages)
	response := map[string]any{"sessionId": info.ID, "toolUseId": id, "agentId": transcript.agentID, "status": status, "messages": messages, "taskNotifications": transcript.notifications, "activityTaskNotifications": activity.notifications, "truncated": truncated || !transcript.historyComplete, "historyComplete": transcript.historyComplete, "activityComplete": transcript.historyComplete, "source": "subagent-jsonl", "canSendMessage": false}
	if truncated {
		response["activityMessages"] = activity.messages
	}
	if transcript.historyComplete {
		if usage := subagentTranscriptUsage(transcript.messages); usage != nil {
			response["usage"] = subagentUsageJSON(usage)
		}
	}
	timestamps := []string{}
	for _, raw := range transcript.messages {
		timestamps = append(timestamps, stringField(historyRecord(raw), "timestamp"))
	}
	for _, notice := range transcript.notifications {
		timestamps = append(timestamps, stringField(notice, "timestamp"))
	}
	if latest := latestSubagentTimestamp(timestamps...); latest != "" {
		response["updatedAt"] = latest
	}
	return response, nil
}
func (server *Server) subagentsHTTP(writer http.ResponseWriter, request *http.Request, info sessionInfo, parts []string) error {
	if len(parts) < 6 {
		return notFound("SubAgent route not found")
	}
	byAgent := parts[4] == "by-agent" && len(parts) == 6
	byTool := parts[4] == "by-tool" && (len(parts) == 6 || len(parts) == 7 && parts[6] == "messages")
	if !byAgent && !byTool {
		return notFound("SubAgent route not found")
	}
	isMessage := byTool && len(parts) == 7
	if !isMessage && request.Method != http.MethodGet || isMessage && request.Method != http.MethodPost {
		return methodNotAllowed(request.Method)
	}
	ref, err := url.PathUnescape(parts[5])
	if err != nil {
		return notFound("SubAgent route not found")
	}
	var response map[string]any
	if byAgent {
		response, err = server.subagentRunByAgent(request.Context(), info, ref)
	} else {
		response, err = server.subagentRunByTool(request.Context(), info, ref, request.URL.Query().Get("taskId"))
	}
	if err != nil {
		return err
	}
	if response == nil {
		return notFound("SubAgent run not found: " + ref)
	}
	if isMessage {
		return server.sendSubagentMessage(writer, request, info.ID, ref, response)
	}
	writeJSON(writer, 200, response)
	return nil
}
