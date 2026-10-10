package teamworker

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func LeaderRuntime(environment map[string]string, fallback string) (protocol.JSONObject, error) {
	input, present := environment["CC_HAHA_TEAM_LEADER_RUNTIME"]
	if !present {
		input = "null"
	}
	value, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(input))
	if err != nil {
		return protocol.JSONObject{}, errors.New("The desktop team runtime is invalid. Reopen the session before planning a team.")
	}
	leader := object(value)
	if !leader.IsObject() || !isString(leader.Get("providerId")) || protocol.TrimJavaScriptString(leader.String("providerId")) == "" {
		return protocol.JSONObject{}, errors.New("The desktop team provider is unavailable. Select a provider before planning a team.")
	}
	model := protocol.TrimJavaScriptString(fallback)
	if model == "" && isString(leader.Get("modelId")) {
		model = protocol.TrimJavaScriptString(leader.String("modelId"))
	}
	if model == "" {
		return protocol.JSONObject{}, errors.New("Select a leader model before planning a team.")
	}
	runtime := protocol.NewJSONObject(protocol.JSONField{Name: "providerId", Value: leader.Get("providerId")}, protocol.JSONField{Name: "modelId", Value: raw(model)})
	if isString(leader.Get("effortLevel")) {
		runtime.Set("effortLevel", leader.Get("effortLevel"))
	}
	return runtime, nil
}

// SnapshotAgents freezes the same active catalog used by Agent. Prompt is only
// called for definitions without a raw system body (such as built-in Guide).
func SnapshotAgents(active []agentcatalog.Definition, prompt func(agentcatalog.Definition) (string, error)) (protocol.JSONObject, error) {
	if prompt == nil {
		return protocol.JSONObject{}, errors.New("Team snapshot requires the actual Agent prompt producer")
	}
	catalog := protocol.NewJSONObject()
	for _, agent := range active {
		identity, err := SnapshotPresetSource(agent)
		if err != nil {
			return catalog, err
		}
		body := agent.Fields.Get("rawSystemPrompt")
		if body == nil || string(body) == "null" {
			text, err := prompt(agent)
			if err != nil {
				return catalog, err
			}
			body = raw(text)
		}
		snapshot := protocol.NewJSONObject(protocol.JSONField{Name: "agentType", Value: raw(agent.Name)}, protocol.JSONField{Name: "source", Value: raw(agent.Source)}, protocol.JSONField{Name: "sourceIdentity", Value: raw(identity)}, protocol.JSONField{Name: "description", Value: raw(agent.Description)}, protocol.JSONField{Name: "systemPrompt", Value: body})
		for _, name := range []string{"tools", "disallowedTools", "model", "permissionMode", "skills", "isolation", "memory", "hooks"} {
			value := agent.Fields.Get(name)
			if value != nil && string(value) != "null" && string(value) != "false" && string(value) != `""` && string(value) != "0" {
				snapshot.Set(name, value)
			}
		}
		if effort := agent.Effort; effort != nil {
			value := string(effort)
			if isString(effort) {
				value = object(raw(map[string]json.RawMessage{"v": effort})).String("v")
			}
			snapshot.Set("effortLevel", raw(value))
		}
		if value := agent.Fields.Get("mcpServers"); value != nil && string(value) != "null" {
			specs := []json.RawMessage{}
			_ = json.Unmarshal(value, &specs)
			names := []json.RawMessage{}
			inline := false
			for _, spec := range specs {
				if isString(spec) {
					names = append(names, spec)
				} else {
					inline = true
				}
			}
			snapshot.Set("mcpServers", raw(names))
			if inline {
				snapshot.Set("configurationError", raw("Inline MCP server configurations are not supported in team plans. Configure the server separately and reference its name in the Agent preset."))
			}
		}
		if value := agent.Fields.Get("initialPrompt"); value != nil && string(value) != `""` && string(value) != "null" {
			snapshot.Set("initialPrompt", value)
		}
		for _, name := range []string{"maxTurns", "omitClaudeMd"} {
			if value := agent.Fields.Get(name); value != nil {
				snapshot.Set(name, value)
			}
		}
		catalog.Set(agent.Name, raw(snapshot))
	}
	return catalog, nil
}

// ResolveProposal carries suggestions only. It cannot approve or launch a
// member; the trusted human API must independently validate its frozen source.
func ResolveProposal(proposal, catalog, leader protocol.JSONObject, environment map[string]string, parseModel func(string) (string, error)) (protocol.JSONObject, error) {
	if parseModel == nil {
		return protocol.JSONObject{}, errors.New("Team proposal requires the real model alias parser")
	}
	members := rows(proposal.Get("members"))
	for i, member := range members {
		agentType := member.String("agentType")
		if agentType == "" {
			agentType = "general-purpose"
		}
		snapshot := object(catalog.Get(agentType))
		if !snapshot.IsObject() {
			return protocol.JSONObject{}, fmt.Errorf("Agent preset '%s' is unavailable. Choose a listed Agent preset.", agentType)
		}
		if message := snapshot.String("configurationError"); message != "" {
			return protocol.JSONObject{}, errors.New(message)
		}
		runtime := object(member.Get("runtime"))
		if !runtime.IsObject() {
			runtime = leader.Clone()
			model := leader.String("modelId")
			configured := protocol.TrimJavaScriptString(environment["CLAUDE_CODE_SUBAGENT_MODEL"])
			selected := protocol.TrimJavaScriptString(snapshot.String("model"))
			if configured != "" && !strings.EqualFold(configured, "inherit") {
				selected = configured
			} else if strings.EqualFold(selected, "inherit") {
				selected = ""
			}
			if selected != "" {
				var err error
				model, err = parseModel(selected)
				if err != nil {
					return protocol.JSONObject{}, err
				}
			}
			runtime.Set("modelId", raw(model))
			if effort := snapshot.Get("effortLevel"); effort != nil {
				runtime.Set("effortLevel", effort)
			} else if model != leader.String("modelId") {
				runtime.Delete("effortLevel")
			}
		}
		next := member.Clone()
		next.Set("agentType", raw(agentType))
		next.Set("runtime", raw(runtime))
		next.Set("suggestedRuntime", raw(runtime.Clone()))
		next.Set("agentSnapshot", raw(snapshot.Clone()))
		members[i] = next
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "members", Value: raw(members)}, protocol.JSONField{Name: "tasks", Value: proposal.Get("tasks")}, protocol.JSONField{Name: "agentCatalog", Value: raw(catalog)}, protocol.JSONField{Name: "leaderRuntime", Value: raw(leader)}), nil
}
func PlanToolResult(plan protocol.JSONObject) protocol.JSONObject {
	members := rows(plan.Get("members"))
	for i, member := range members {
		member.Delete("agentSnapshot")
		members[i] = member
	}
	message := "The plan is " + plan.String("state") + ". Do not recreate or replay its members. Inspect the team panel for execution status."
	if plan.String("state") == "review_pending" {
		message = "The proposed members are awaiting human review and have not started. Existing approved members may continue. End this planning turn and wait for the user to confirm in the team panel."
	} else if plan.String("state") == "draft" {
		message = "The proposed members have not started. Finish the roster and tasks, then call TeamPlan with operation=\"submit\" and this revision."
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "team_name", Value: plan.Get("teamName")}, protocol.JSONField{Name: "plan_id", Value: plan.Get("planId")}, protocol.JSONField{Name: "revision", Value: plan.Get("revision")}, protocol.JSONField{Name: "state", Value: plan.Get("state")}, protocol.JSONField{Name: "members", Value: raw(members)}, protocol.JSONField{Name: "tasks", Value: plan.Get("tasks")}, protocol.JSONField{Name: "message", Value: raw(message)})
}
func CaptureLegacyTasks(plan protocol.JSONObject, tasks *taskstore.Service) ([]protocol.JSONObject, error) {
	existing := rows(plan.Get("tasks"))
	if len(existing) > 0 {
		return existing, nil
	}
	if tasks == nil {
		return nil, errors.New("Team plan requires actual task storage")
	}
	physical, err := tasks.List(teamstore.SanitizeName(plan.String("teamName")))
	if err != nil {
		return nil, err
	}
	members := rows(plan.Get("members"))
	proposed := []protocol.JSONObject{}
	ids := map[string]bool{}
	for _, task := range physical {
		if task.String("status") != "pending" || object(task.Get("metadata")).String("teamPlanId") != "" {
			continue
		}
		for _, member := range members {
			if member.String("name") == task.String("owner") {
				proposed = append(proposed, task)
				ids[task.String("id")] = true
				break
			}
		}
	}
	result := []protocol.JSONObject{}
	for _, task := range proposed {
		dependencies := []string{}
		for _, id := range stringsIn(task.Get("blockedBy")) {
			if ids[id] {
				dependencies = append(dependencies, id)
				continue
			}
			completed := false
			for _, dependency := range physical {
				if dependency.String("id") == id {
					completed = dependency.String("status") == "completed"
					break
				}
			}
			if !completed {
				return nil, fmt.Errorf("Task '%s' depends on work outside this proposal. Submit an explicit complete plan with TeamPlan.", task.String("subject"))
			}
		}
		var owner json.RawMessage
		for _, member := range members {
			if member.String("name") == task.String("owner") {
				owner = member.Get("id")
				break
			}
		}
		result = append(result, protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: task.Get("id")}, protocol.JSONField{Name: "subject", Value: task.Get("subject")}, protocol.JSONField{Name: "description", Value: task.Get("description")}, protocol.JSONField{Name: "ownerId", Value: owner}, protocol.JSONField{Name: "dependencies", Value: raw(dependencies)}))
	}
	return result, nil
}
