package teamspawn

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

const InteractiveInboxPollMilliseconds = 1000

type InteractiveInbox struct {
	Mailbox *teamstore.Mailbox
	Config  *teamstore.Config
	Tasks   *taskstore.Service
}

type InteractiveInboxRead struct {
	Unread                  int
	Permissions             LeadMailboxPartition
	Regular, Plans, Removed []protocol.JSONObject
	DeferShutdown           bool
	Candidates              []json.RawMessage
}

// Read routes Source useInboxPoller's leader-side protocols against actual
// leader-owned team files. Delivery and read marking are separate: the owner
// must first admit permissions, plan responses and regular inbox messages.
func (l InteractiveInbox) Read(team string) (InteractiveInboxRead, error) {
	result := InteractiveInboxRead{Permissions: PartitionLeadMailboxMessages(nil), Regular: []protocol.JSONObject{}, Plans: []protocol.JSONObject{}, Removed: []protocol.JSONObject{}}
	if l.Mailbox == nil || l.Config == nil || l.Tasks == nil {
		return result, errors.New("interactive inbox requires physical mailbox/config/tasks")
	}
	unread, err := l.Mailbox.Unread("team-lead", team)
	if err != nil {
		return result, err
	}
	candidates := []json.RawMessage{}
	for _, v := range unread {
		message := inboxObject(v)
		if ShutdownApproved(message.String("text")) != nil && l.Config.ReadAsync(team) == nil {
			result.DeferShutdown = true
			continue
		}
		candidates = append(candidates, v)
	}
	unread, err = l.Mailbox.Claim("team-lead", team, candidates)
	if err != nil {
		return result, err
	}
	result.Unread = len(unread)
	result.Candidates = unread
	var trusted *protocol.JSONObject
	trustedRead := false
	for _, value := range unread {
		message := inboxObject(value)
		kind, parsed := ParseInteractiveProtocol(message.String("text"))
		entry := protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: rawJSON(message)}, protocol.JSONField{Name: "parsed", Value: rawJSON(parsed)})
		switch kind {
		case "permission_request":
			result.Permissions.PermissionRequests = append(result.Permissions.PermissionRequests, entry)
		case "sandbox_permission_request":
			result.Permissions.SandboxPermissionRequests = append(result.Permissions.SandboxPermissionRequests, entry)
		case "permission_response", "sandbox_permission_response", "shutdown_request", "team_permission_update", "mode_set_request":
			// Leader has no worker callbacks. Valid protocol messages are consumed.
		case "plan_approval_request":
			result.Plans = append(result.Plans, entry)
		case "shutdown_approved":
			if !trustedRead {
				trusted = l.Config.ReadAsync(team)
				trustedRead = true
			}
			if trusted == nil {
				result.DeferShutdown = true
				continue
			}
			if parsed.String("from") != message.String("from") {
				continue
			}
			var members []protocol.JSONObject
			_ = json.Unmarshal(trusted.Get("members"), &members)
			var member protocol.JSONObject
			for _, row := range members {
				if row.String("name") == message.String("from") {
					member = row
					break
				}
			}
			if !member.IsObject() {
				continue
			}
			result.Removed = append(result.Removed, member.Clone())
			result.Regular = append(result.Regular, message.Clone())
		default:
			result.Regular = append(result.Regular, message.Clone())
		}
	}
	// Requests are appended after ordinary chat, as in the hook's handler order.
	for _, entry := range result.Plans {
		message, _ := entry.Object("message")
		result.Regular = append(result.Regular, message)
	}
	// Approvals were classified earlier but the hook handles them after plans.
	approvals, ordinary := []protocol.JSONObject{}, []protocol.JSONObject{}
	for _, row := range result.Regular {
		kind, _ := ParseInteractiveProtocol(row.String("text"))
		if kind == "shutdown_approved" {
			approvals = append(approvals, row)
		} else {
			ordinary = append(ordinary, row)
		}
	}
	result.Regular = append(ordinary, approvals...)
	return result, nil
}

func (l InteractiveInbox) MarkDelivered(team string, deferShutdown bool, candidates ...[]json.RawMessage) error {
	if len(candidates) == 0 {
		return errors.New("interactive delivery requires the immutable accepted mailbox batch")
	}
	delivered := []json.RawMessage{}
	for _, v := range candidates[0] {
		if !deferShutdown || ShutdownApproved(inboxObject(v).String("text")) == nil {
			delivered = append(delivered, v)
		}
	}
	_, e := l.Mailbox.Claim("team-lead", team, delivered)
	return e
}

func (l InteractiveInbox) RemoveApproved(team string, member protocol.JSONObject) (protocol.JSONObject, error) {
	name, id := member.String("name"), member.String("agentId")
	if _, err := l.Config.RemoveMember(team, id, name); err != nil {
		return protocol.JSONObject{}, err
	}
	result, err := l.Tasks.UnassignTeammateTasks(team, id, name, "shutdown")
	if err != nil {
		return protocol.JSONObject{}, err
	}
	body := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("teammate_terminated")}, protocol.JSONField{Name: "message", Value: quoted(result.NotificationMessage)})
	return protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoted("system")}, protocol.JSONField{Name: "text", Value: quoted(string(rawJSON(body)))}), nil
}
