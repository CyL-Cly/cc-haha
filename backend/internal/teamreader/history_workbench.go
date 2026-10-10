package teamreader

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type projectedTeam struct {
	name, lead       string
	created          float64
	detail           protocol.JSONObject
	members          []protocol.JSONObject
	memberIndices    map[string]int
	tasks            *orderedTasks
	messages         []protocol.JSONObject
	updated, deleted string
}

func (t *projectedTeam) setMember(id string, member protocol.JSONObject) {
	if i, ok := t.memberIndices[id]; ok {
		t.members[i] = member
	} else {
		t.memberIndices[id] = len(t.members)
		t.members = append(t.members, member)
	}
}

var teammateMessagePattern = regexp.MustCompile(`(?s)<teammate-message[\s\x{00a0}\x{feff}]+teammate_id="([^"]+)"(?:[\s\x{00a0}\x{feff}]+color="([^"]+)")?[^>]*>(.*?)</teammate-message>`)

func (s *Service) ProjectWorkbenches(session string, messages []protocol.JSONObject) []protocol.JSONObject {
	results := resultRecords(messages)
	ordered := s.orderedMessages(messages)
	teams := []*projectedTeam{}
	var current *projectedTeam
	for _, message := range ordered {
		timestamp := message.String("timestamp")
		if timestamp == "" || !finite(s.parsedDate(timestamp)) {
			timestamp = s.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		}
		if message.String("type") == "user" && current != nil {
			for _, match := range teammateMessagePattern.FindAllStringSubmatch(transcriptText(message.Get("content")), -1) {
				body := protocol.TrimJavaScriptString(match[3])
				if body == "" {
					continue
				}
				item := protocol.NewJSONObject(field("id", fmt.Sprintf("%s:teammate:%d", message.String("id"), len(current.messages))), field("from", match[1]), field("to", "team-lead"), field("recipients", []string{"team-lead"}), field("kind", "direct"), field("text", body), field("timestamp", timestamp))
				if match[2] != "" {
					item.Set("color", raw(match[2]))
				}
				current.messages = append(current.messages, item)
				current.updated = timestamp
			}
		}
		if message.String("type") != "tool_use" {
			continue
		}
		for _, block := range rows(message.Get("content")) {
			if block.String("type") != "tool_use" || !nonblank(block.Get("id")) || !nonblank(block.Get("name")) {
				continue
			}
			id, name := block.String("id"), block.String("name")
			input := obj(block.Get("input"))
			result, hasResult := results[id]
			if name == "TeamCreate" {
				if hasResult && !successful(result, true) {
					continue
				}
				rawName := stringFallback(result.value.Get("team_name"), input.Get("team_name"))
				if rawName == nil {
					continue
				}
				teamName := obj(raw(map[string]any{"v": rawName})).String("v")
				lead := stringFallback(result.value.Get("lead_agent_id"), raw("team-lead@"+teamName))
				leadID := obj(raw(map[string]any{"v": lead})).String("v")
				team := &projectedTeam{name: teamName, lead: leadID, created: s.parsedDate(timestamp), detail: protocol.NewJSONObject(), memberIndices: map[string]int{}, tasks: newOrderedTasks(), messages: []protocol.JSONObject{}, updated: timestamp}
				team.detail.Set("description", stringFallback(input.Get("description")))
				agentType := stringFallback(input.Get("agent_type"), raw("team-lead"))
				cwd := stringFallback(message.Get("cwd"))
				if cwd == nil {
					cwd = raw("")
				}
				member := protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: lead}, field("name", "team-lead"), protocol.JSONField{Name: "agentType", Value: agentType}, field("status", "completed"), field("activity", "exited"), field("joinedAt", team.created), protocol.JSONField{Name: "cwd", Value: cwd}, field("sessionId", session))
				team.setMember(leadID, member)
				teams = append(teams, team)
				current = team
				continue
			}
			var team *projectedTeam
			explicit := obj(raw(map[string]any{"v": stringFallback(input.Get("team_name"))})).String("v")
			if current != nil && (explicit == "" || current.name == explicit) {
				team = current
			} else {
				for i := len(teams) - 1; i >= 0; i-- {
					if teams[i].name == explicit {
						team = teams[i]
						break
					}
				}
			}
			if team == nil {
				continue
			}
			team.updated = timestamp
			if hasResult && !successful(result, true) {
				continue
			}
			switch name {
			case "TeamDelete":
				if value := result.value.Get("finalTasks"); len(value) > 0 && value[0] == '[' {
					replaceTaskList(team.tasks, value, team.name, false)
				}
				team.deleted = timestamp
				if current == team {
					current = nil
				}
			case "Agent":
				memberName := stringFallback(input.Get("name"))
				if memberName == nil {
					continue
				}
				nameText := obj(raw(map[string]any{"v": memberName})).String("v")
				agentID := stringFallback(result.value.Get("agent_id"), result.value.Get("teammate_id"), raw(nameText+"@"+team.name))
				agentText := obj(raw(map[string]any{"v": agentID})).String("v")
				member := protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: agentID}, protocol.JSONField{Name: "name", Value: memberName}, protocol.JSONField{Name: "agentType", Value: stringFallback(result.value.Get("agent_type"), input.Get("subagent_type"))}, protocol.JSONField{Name: "model", Value: stringFallback(result.value.Get("model"))}, protocol.JSONField{Name: "color", Value: stringFallback(result.value.Get("color"))})
				if result.value.String("tmux_session_name") == "in-process" {
					member.Set("backendType", raw("in-process"))
				}
				member.Set("status", raw("completed"))
				member.Set("activity", raw("exited"))
				member.Set("joinedAt", raw(s.parsedDate(timestamp)))
				cwd := stringFallback(message.Get("cwd"))
				if cwd == nil {
					cwd = raw("")
				}
				member.Set("cwd", cwd)
				team.setMember(agentText, member)
			case "TaskCreate":
				taskResult := obj(result.value.Get("task"))
				if !nonblank(taskResult.Get("id")) {
					continue
				}
				taskID := taskResult.String("id")
				task := applyTaskUpdate(team.tasks.ensure(taskID, team.name), input)
				if subject := stringFallback(taskResult.Get("subject")); subject != nil {
					task.Set("subject", subject)
				}
				team.tasks.set(taskID, task)
			case "TaskUpdate":
				if !nonblank(input.Get("taskId")) {
					continue
				}
				taskID := input.String("taskId")
				if input.String("status") == "deleted" {
					team.tasks.delete(taskID)
				} else {
					team.tasks.set(taskID, applyTaskUpdate(team.tasks.ensure(taskID, team.name), input))
				}
			case "TaskList":
				if value := result.value.Get("tasks"); len(value) > 0 && value[0] == '[' {
					replaceTaskList(team.tasks, value, team.name, true)
				}
			case "SendMessage":
				text := stringFallback(input.Get("message"), input.Get("content"))
				if text == nil {
					continue
				}
				target := stringFallback(input.Get("to"), input.Get("recipient"), raw("team-lead"))
				targetText := obj(raw(map[string]any{"v": target})).String("v")
				broadcast := targetText == "*" || input.String("type") == "broadcast"
				recipients := []string{targetText}
				kind, to := "direct", target
				if broadcast {
					kind, to = "broadcast", raw("*")
					recipients = []string{}
					for _, member := range team.members {
						v := member.Get("name")
						if v == nil || string(v) == "null" {
							v = member.Get("agentId")
						}
						recipient := obj(raw(map[string]any{"v": v})).String("v")
						if recipient != "team-lead" {
							recipients = append(recipients, recipient)
						}
					}
				}
				team.messages = append(team.messages, protocol.NewJSONObject(field("id", id), field("from", "team-lead"), protocol.JSONField{Name: "to", Value: to}, field("recipients", recipients), field("kind", kind), protocol.JSONField{Name: "text", Value: text}, protocol.JSONField{Name: "summary", Value: stringFallback(input.Get("summary"))}, field("timestamp", timestamp)))
			}
		}
	}
	out := []protocol.JSONObject{}
	for _, team := range teams {
		finalizeTaskEdges(team.tasks)
		tasks := team.tasks.all()
		sortTasks(tasks)
		detail := protocol.NewJSONObject(field("name", team.name), protocol.JSONField{Name: "description", Value: team.detail.Get("description")}, field("createdAt", team.created))
		detail.Set("incarnationId", raw(incarnation(protocol.NewJSONObject(field("name", team.name), field("createdAt", team.created), field("leadSessionId", session)))))
		detail.Set("memberCount", raw(len(team.members)))
		detail.Set("activeMemberCount", raw(0))
		detail.Set("leadAgentId", raw(team.lead))
		detail.Set("leadSessionId", raw(session))
		detail.Set("members", raw(team.members))
		canonical := protocol.StringifyValidatedJavaScriptJSON(raw(protocol.NewJSONObject(field("team", detail), field("tasks", tasks), field("messages", team.messages))))
		sort.SliceStable(team.messages, func(i, j int) bool {
			a, b := team.messages[i], team.messages[j]
			if cmp := localeCompare(a.String("timestamp"), b.String("timestamp")); cmp != 0 {
				return cmp < 0
			}
			return localeCompare(a.String("id"), b.String("id")) < 0
		})
		deleted := team.deleted
		if deleted == "" {
			deleted = team.updated
		}
		out = append(out, protocol.NewJSONObject(field("version", "transcript:"+hash(canonical)), field("generatedAt", team.updated), field("deletedAt", deleted), field("team", detail), field("tasks", tasks), field("messages", team.messages)))
	}
	return out
}
