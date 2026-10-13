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
	Now                 func() time.Time
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
	if o.Now == nil {
		o.Now = time.Now
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

// Wait preserves pending user input, then shutdown or mailbox batch, then task
// priority. Reading and claiming share the physical mailbox transaction.
type WaitOptions struct {
	Deadline      time.Time
	CancelContext context.Context
	ClaimTasks    *bool
}

func (s *Inbox) Wait(ctx context.Context, options ...WaitOptions) (protocol.JSONObject, error) {
	o := WaitOptions{}
	if len(options) > 0 {
		o = options[0]
	}
	sleepContext, cancelSleep := context.WithCancel(ctx)
	defer cancelSleep()
	if o.CancelContext != nil {
		stop := context.AfterFunc(o.CancelContext, cancelSleep)
		defer stop()
	}
	interrupted := func() *protocol.JSONObject {
		if ctx.Err() != nil {
			value := abortedPrompt()
			return &value
		}
		if o.CancelContext != nil && o.CancelContext.Err() != nil {
			value := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("cancelled")})
			return &value
		}
		return nil
	}
	polls := 0
	for {
		if result := interrupted(); result != nil {
			return *result, nil
		}
		pending, ok, err := s.options.ReadPending(ctx)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if ok {
			return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("new_message")}, protocol.JSONField{Name: "message", Value: quoted(pending)}, protocol.JSONField{Name: "from", Value: quoted("user")}), nil
		}
		if polls > 0 {
			wait := 500 * time.Millisecond
			if !o.Deadline.IsZero() && o.Deadline.Sub(s.options.Now()) < wait {
				wait = o.Deadline.Sub(s.options.Now())
			}
			if wait > 0 {
				if err = s.options.Sleep(sleepContext, wait); err != nil {
					if result := interrupted(); result != nil {
						return *result, nil
					}
					return protocol.JSONObject{}, err
				}
			}
		}
		polls++
		if result := interrupted(); result != nil {
			return *result, nil
		}
		message, err := s.pollMailbox()
		if err != nil {
			s.options.Log(err)
		} else if message != nil {
			return *message, nil
		}
		if o.ClaimTasks == nil || *o.ClaimTasks {
			prompt, err := s.claimNext()
			if err != nil {
				s.options.Log(err)
			} else if prompt != nil {
				return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("new_message")}, protocol.JSONField{Name: "message", Value: *prompt}, protocol.JSONField{Name: "from", Value: quoted("task-list")}), nil
			}
		}
		if !o.Deadline.IsZero() && !s.options.Now().Before(o.Deadline) {
			return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("timeout")}), nil
		}
	}
}

// PollMailbox drains every deliverable message as a single next prompt. Protocol
// frames are claimed without model delivery; a shutdown is delivered first alone.
func (s *Inbox) PollMailbox() (*protocol.JSONObject, error) { return s.pollMailbox() }
func (s *Inbox) pollMailbox() (*protocol.JSONObject, error) {
	unread, e := s.options.Mailbox.Unread(s.options.AgentName, s.options.TeamName)
	if e != nil {
		return nil, e
	}
	for _, data := range unread {
		row := inboxObject(data)
		request := shutdownRequest(row.String("text"))
		if request == nil {
			continue
		}
		claimed, e := s.options.Mailbox.Claim(s.options.AgentName, s.options.TeamName, []json.RawMessage{data})
		if e != nil {
			return nil, e
		}
		if len(claimed) == 0 {
			return nil, nil
		}
		from := request.String("from")
		if from == "" {
			from = row.String("from")
		}
		if from == "" {
			from = "team-lead"
		}
		result := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("shutdown_request")}, protocol.JSONField{Name: "from", Value: quoted(from)}, protocol.JSONField{Name: "originalMessage", Value: row.Get("text")})
		return &result, nil
	}
	deliverable, frames := []json.RawMessage{}, []json.RawMessage{}
	for _, data := range unread {
		row := inboxObject(data)
		kind, _ := ParseInteractiveProtocol(row.String("text"))
		if !StructuredProtocolMessage(row.String("text")) || row.String("from") == "team-lead" && kind == "plan_approval_response" {
			deliverable = append(deliverable, data)
		} else {
			frames = append(frames, data)
		}
	}
	if len(frames) > 0 {
		if _, e = s.options.Mailbox.Claim(s.options.AgentName, s.options.TeamName, frames); e != nil {
			return nil, e
		}
	}
	if len(deliverable) == 0 {
		return nil, nil
	}
	claimed, e := s.options.Mailbox.Claim(s.options.AgentName, s.options.TeamName, deliverable)
	if e != nil {
		return nil, e
	}
	if len(claimed) == 0 {
		return nil, nil
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("new_messages")}, protocol.JSONField{Name: "messages", Value: rawJSON(claimed)})
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
