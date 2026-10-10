package teamspawn

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"time"
)

type InboxOptions struct {
	Mailbox             *teamstore.Mailbox
	Tasks               *taskstore.Service
	TeamName, AgentName string
	ReadPending         func(context.Context) (string, bool, error)
	Sleep               func(context.Context, time.Duration) error
	Log                 func(error)
}
type Inbox struct{ options InboxOptions }

func NewInbox(o InboxOptions) (*Inbox, error) {
	if o.Mailbox == nil || o.Tasks == nil || o.TeamName == "" || o.AgentName == "" || o.ReadPending == nil || o.Log == nil {
		return nil, errors.New("in-process inbox requires actual team/task/mailbox/pending-message producers")
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
	}
	return &Inbox{options: o}, nil
}
func inboxObject(raw json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(raw)
	return value
}
func inboxTruthy(value json.RawMessage) bool {
	if len(value) == 0 || string(value) == "null" || string(value) == "false" || string(value) == "0" || string(value) == `""` {
		return false
	}
	return true
}
func shutdownRequest(text string) *protocol.JSONObject {
	value := inboxObject([]byte(text))
	if value.String("type") != "shutdown_request" {
		return nil
	}
	for _, name := range []string{"requestId", "from", "timestamp"} {
		if _, err := protocol.DecodeJavaScriptJSONString(value.Get(name)); err != nil {
			return nil
		}
	}
	if reason := value.Get("reason"); reason != nil {
		if _, err := protocol.DecodeJavaScriptJSONString(reason); err != nil {
			return nil
		}
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("shutdown_request")}, protocol.JSONField{Name: "requestId", Value: value.Get("requestId")}, protocol.JSONField{Name: "from", Value: value.Get("from")}, protocol.JSONField{Name: "reason", Value: value.Get("reason")}, protocol.JSONField{Name: "timestamp", Value: value.Get("timestamp")})
	return &result
}
func abortedPrompt() protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("aborted")})
}

// Wait preserves original pending-user -> shutdown -> leader -> FIFO -> task
// priority. Reading and marking use the original shared physical mailbox.
func (s *Inbox) Wait(ctx context.Context) (protocol.JSONObject, error) {
	polls := 0
	for ctx.Err() == nil {
		pending, ok, err := s.options.ReadPending(ctx)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if ok {
			return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("new_message")}, protocol.JSONField{Name: "message", Value: quoted(pending)}, protocol.JSONField{Name: "from", Value: quoted("user")}), nil
		}
		if polls > 0 {
			if err = s.options.Sleep(ctx, 500*time.Millisecond); err != nil {
				if ctx.Err() != nil {
					return abortedPrompt(), nil
				}
				return protocol.JSONObject{}, err
			}
		}
		polls++
		if ctx.Err() != nil {
			return abortedPrompt(), nil
		}
		message, err := s.pollMailbox()
		if err != nil {
			s.options.Log(err)
		} else if message != nil {
			return *message, nil
		}
		prompt, err := s.claimNext()
		if err != nil {
			s.options.Log(err)
		} else if prompt != nil {
			return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("new_message")}, protocol.JSONField{Name: "message", Value: *prompt}, protocol.JSONField{Name: "from", Value: quoted("task-list")}), nil
		}
	}
	return abortedPrompt(), nil
}
func (s *Inbox) pollMailbox() (*protocol.JSONObject, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(s.options.Mailbox.Read(s.options.AgentName, s.options.TeamName), &raw); err != nil {
		return nil, err
	}
	rows := make([]protocol.JSONObject, len(raw))
	for i, row := range raw {
		rows[i] = inboxObject(row)
	}
	for i, row := range rows {
		if !row.IsObject() || inboxTruthy(row.Get("read")) {
			continue
		}
		request := shutdownRequest(row.String("text"))
		if request == nil {
			continue
		}
		if err := s.options.Mailbox.MarkIndex(s.options.AgentName, s.options.TeamName, i); err != nil {
			return nil, err
		}
		result := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("shutdown_request")}, protocol.JSONField{Name: "request", Value: rawJSON(*request)}, protocol.JSONField{Name: "originalMessage", Value: row.Get("text")})
		return &result, nil
	}
	selected := -1
	for i, row := range rows {
		if row.IsObject() && !inboxTruthy(row.Get("read")) && row.String("from") == "team-lead" {
			selected = i
			break
		}
	}
	if selected < 0 {
		for i, row := range rows {
			if !row.IsObject() {
				return nil, errors.New("Cannot read properties of null (reading 'read')")
			}
			if !inboxTruthy(row.Get("read")) {
				selected = i
				break
			}
		}
	}
	if selected < 0 {
		return nil, nil
	}
	row := rows[selected]
	if err := s.options.Mailbox.MarkIndex(s.options.AgentName, s.options.TeamName, selected); err != nil {
		return nil, err
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("new_message")}, protocol.JSONField{Name: "message", Value: row.Get("text")}, protocol.JSONField{Name: "from", Value: row.Get("from")}, protocol.JSONField{Name: "color", Value: row.Get("color")}, protocol.JSONField{Name: "summary", Value: row.Get("summary")})
	return &result, nil
}
func rawJSON(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func (s *Inbox) claimNext() (*json.RawMessage, error) {
	list := teamstore.SanitizeName(s.options.TeamName)
	tasks, err := s.options.Tasks.List(list)
	if err != nil {
		return nil, err
	}
	owns := false
	unresolved := map[string]bool{}
	for _, task := range tasks {
		if task.String("owner") == s.options.AgentName {
			owns = true
		}
		if task.String("status") != "completed" {
			unresolved[string(task.Get("id"))] = true
		}
	}
	if !owns {
		return nil, nil
	}
	for _, task := range tasks {
		if task.String("status") != "pending" || inboxTruthy(task.Get("owner")) {
			continue
		}
		blocked := false
		var edges []json.RawMessage
		_ = json.Unmarshal(task.Get("blockedBy"), &edges)
		for _, edge := range edges {
			if unresolved[string(edge)] {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		result, err := s.options.Tasks.Claim(list, task.String("id"), s.options.AgentName, taskstore.ClaimOptions{CheckAgentBusy: true, MarkInProgress: true})
		if err != nil {
			return nil, err
		}
		if string(result.Get("success")) != "true" {
			continue
		}
		parts := []json.RawMessage{quoted("Complete all open tasks. Start with task #"), task.Get("id"), quoted(": \n\n "), task.Get("subject")}
		if inboxTruthy(task.Get("description")) {
			parts = append(parts, quoted("\n\n"), task.Get("description"))
		}
		prompt, err := protocol.JoinJavaScriptValuesJSON(parts, "")
		if err != nil {
			return nil, err
		}
		return &prompt, nil
	}
	return nil, nil
}
