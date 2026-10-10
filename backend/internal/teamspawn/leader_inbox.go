package teamspawn

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

type LeaderInbox struct {
	Mailbox *teamstore.Mailbox
	Config  *teamstore.Config
	Tasks   *taskstore.Service
}
type LeadMailboxRead struct {
	Partition LeadMailboxPartition
	Snapshot  protocol.JSONObject
	Formatted json.RawMessage
	Removed   []string
}

// Read performs one original print.ts leader poll. Read marking precedes
// classification; shutdown cleanup uses the refreshed roster, then preserves
// ordinary approval/idle messages in the model-visible XML.
func (l LeaderInbox) Read(team string, snapshot protocol.JSONObject) (LeadMailboxRead, error) {
	result := LeadMailboxRead{Partition: PartitionLeadMailboxMessages(nil), Snapshot: snapshot.Clone()}
	if l.Mailbox == nil || l.Config == nil || l.Tasks == nil {
		return result, errors.New("leader inbox requires original physical producers")
	}
	unread, err := l.Mailbox.Unread("team-lead", team)
	if err != nil {
		return result, err
	}
	if len(unread) == 0 {
		return result, nil
	}
	if err = l.Mailbox.MarkAll("team-lead", team); err != nil {
		return result, err
	}
	messages := make([]protocol.JSONObject, len(unread))
	for i, value := range unread {
		messages[i] = inboxObject(value)
	}
	result.Partition = PartitionLeadMailboxMessages(messages)
	refreshed, ok := snapshot.Object("teammates")
	if !ok {
		refreshed = protocol.NewJSONObject()
	}
	for _, message := range result.Partition.Remaining {
		approved := ShutdownApproved(message.String("text"))
		if approved == nil || team == "" {
			continue
		}
		name, id := approved.String("from"), ""
		for _, property := range refreshed.OwnProperties() {
			var member protocol.JSONObject
			_ = member.UnmarshalJSON(property.Value)
			if member.String("name") == name {
				_ = json.Unmarshal(property.NameJSON, &id)
				break
			}
		}
		if id == "" {
			continue
		}
		if _, err = l.Config.RemoveMember(team, id, name); err != nil {
			return result, err
		}
		if _, err = l.Tasks.UnassignTeammateTasks(team, id, name, "shutdown"); err != nil {
			return result, err
		}
		result.Removed = append(result.Removed, id)
		current, ok := result.Snapshot.Object("teammates")
		if ok {
			current = current.Clone()
			current.Delete(id)
			result.Snapshot.Set("teammates", rawJSON(current))
		}
	}
	if len(result.Partition.Remaining) > 0 {
		result.Formatted, err = FormatHeadlessTeammateMessages(result.Partition.Remaining)
	}
	return result, err
}

func HasTeammatesRequiringShutdown(snapshot protocol.JSONObject, tasks []protocol.JSONObject) bool {
	for _, task := range tasks {
		if task.String("type") == "in_process_teammate" && task.String("status") == "running" {
			return true
		}
	}
	roster, ok := snapshot.Object("teammates")
	if !ok {
		return false
	}
	for _, field := range roster.OwnProperties() {
		member := inboxObject(field.Value)
		if member.String("name") != "team-lead" {
			return true
		}
	}
	return false
}
