package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

func taskTool(name string) bool {
	return name == "TaskCreate" || name == "TaskGet" || name == "TaskUpdate" || name == "TaskList"
}
func sourceLowerTrim(value string) string {
	return strings.ToLower(strings.Trim(value, "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"))
}
func (s *Service) taskEnabled() bool {
	if s.options.TaskStore == nil || s.options.TaskListID == nil || s.options.ExpandTaskView == nil || s.options.TaskHook == nil && s.options.TaskHookWithContext == nil {
		return false
	}
	return !s.options.NonInteractive || taskEnvironmentTruthy(s.options.Environment["CLAUDE_CODE_ENABLE_TASKS"])
}
func taskEnvironmentTruthy(value string) bool {
	switch sourceLowerTrim(value) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Every phase settles on the existing owner. Physical storage uses the shared
// NativeBinding; hooks and AppState updates never run on the I/O goroutine.
func taskThen[T any](scope *cooperative.Scope, future *ownerasync.OwnerFuture[T], out *ownerasync.OwnerFuture[queryloop.ToolCallResult], next func(*cooperative.Scope, T) error) error {
	_, err := future.Then(scope, func(callback *cooperative.Scope, value T, failure error) error {
		if failure == nil {
			failure = next(callback, value)
		}
		if failure != nil {
			return out.Reject(callback, failure)
		}
		return nil
	})
	return err
}
func resolveTask(scope *cooperative.Scope, out *ownerasync.OwnerFuture[queryloop.ToolCallResult], data json.RawMessage) error {
	return out.Resolve(scope, queryloop.ToolCallResult{Data: data})
}
func taskMissing(id json.RawMessage) json.RawMessage {
	return object(field("success", false), protocol.JSONField{Name: "taskId", Value: id}, field("updatedFields", []string{}), field("error", "Task not found"))
}
func (s *Service) taskHook(ctx context.Context, scope *cooperative.Scope, event string, task protocol.JSONObject, call queryloop.JSONToolCallContext) error {
	if s.options.TaskHookWithContext != nil {
		return s.options.TaskHookWithContext(ctx, scope, event, task, call)
	}
	return s.options.TaskHook(ctx, scope, event, task)
}
func (s *Service) startTask(ctx context.Context, name string, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if err := s.check(scope); err != nil {
		return nil, err
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if !s.taskEnabled() {
		return nil, capability("TaskV2 actual store, identity, hooks and AppState")
	}
	agentID := ""
	if opaque, ok := call.Opaque.(*builtinwrite.CallContext); ok && opaque != nil {
		agentID = opaque.AgentID
	}
	list, err := s.options.TaskListID(scope, agentID)
	if err != nil {
		return nil, err
	}
	out, err := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](s.options.Owner)
	if err != nil {
		return nil, err
	}
	switch name {
	case "TaskCreate":
		data := protocol.NewJSONObject(protocol.JSONField{Name: "subject", Value: in.Get("subject")}, protocol.JSONField{Name: "description", Value: in.Get("description")}, protocol.JSONField{Name: "activeForm", Value: in.Get("activeForm")}, field("status", "pending"), field("owner", nil), field("blocks", []string{}), field("blockedBy", []string{}), protocol.JSONField{Name: "metadata", Value: in.Get("metadata")})
		// An undefined owner is omitted by Source JSON.stringify.
		data.Set("owner", nil)
		future, e := native(s, scope, func() (taskstore.Creation, error) { return s.options.TaskStore.Create(list, data) })
		if e != nil {
			return nil, e
		}
		err = taskThen(scope, future, out, func(callback *cooperative.Scope, creation taskstore.Creation) error {
			task := data.Clone()
			task.Set("id", quote(creation.TaskID))
			if failure := s.taskHook(ctx, callback, "TaskCreated", task, call); failure != nil {
				rollback, e := native(s, callback, func() (taskstore.Deletion, error) { return s.options.TaskStore.Delete(list, quote(creation.TaskID)) })
				if e != nil {
					return e
				}
				return taskThen(callback, rollback, out, func(done *cooperative.Scope, _ taskstore.Deletion) error { return out.Reject(done, failure) })
			}
			if e := s.options.ExpandTaskView(callback); e != nil {
				return e
			}
			return resolveTask(callback, out, object(protocol.JSONField{Name: "task", Value: object(field("id", creation.TaskID), protocol.JSONField{Name: "subject", Value: in.Get("subject")})}, field("taskListMutationAt", creation.CommittedAt), field("taskListMutationRevision", creation.Revision)))
		})
	case "TaskGet":
		future, e := native(s, scope, func() (*protocol.JSONObject, error) { return s.options.TaskStore.Get(list, in.Get("taskId")) })
		if e != nil {
			return nil, e
		}
		err = taskThen(scope, future, out, func(callback *cooperative.Scope, task *protocol.JSONObject) error {
			value := json.RawMessage("null")
			if task != nil {
				projected := protocol.NewJSONObject()
				for _, key := range []string{"id", "subject", "description", "status", "blocks", "blockedBy"} {
					projected.Set(key, task.Get(key))
				}
				value = raw(projected)
			}
			return resolveTask(callback, out, object(protocol.JSONField{Name: "task", Value: value}))
		})
	case "TaskList":
		future, e := native(s, scope, func() (taskstore.Snapshot, error) { return s.options.TaskStore.Snapshot(list) })
		if e != nil {
			return nil, e
		}
		err = taskThen(scope, future, out, func(callback *cooperative.Scope, snapshot taskstore.Snapshot) error {
			return resolveTask(callback, out, taskListResult(snapshot))
		})
	case "TaskUpdate":
		if e := s.options.ExpandTaskView(scope); e != nil {
			return nil, e
		}
		team, member := "", ""
		teammate := s.options.Team != nil && s.options.Team.IsTeammate
		if s.options.Team != nil && s.options.Team.TeamName != nil {
			team, err = s.options.Team.TeamName(scope)
			if err != nil {
				return nil, err
			}
		}
		if teammate && s.options.Team.AgentName != nil {
			member, err = s.options.Team.AgentName(scope)
			if err != nil {
				return nil, err
			}
		}
		type checkedTask struct {
			task    *protocol.JSONObject
			refusal string
		}
		future, e := native(s, scope, func() (checkedTask, error) {
			previous, e := s.options.TaskStore.Get(list, in.Get("taskId"))
			if e != nil {
				return checkedTask{}, e
			}
			status := in.String("status")
			if team != "" && (status == "in_progress" || status == "completed") {
				if s.options.Team.TaskExecutionApproved == nil {
					return checkedTask{}, capability("Team task approval storage")
				}
				owner := in.String("owner")
				if in.Get("owner") == nil && previous != nil {
					owner = previous.String("owner")
				}
				approved, e := s.options.Team.TaskExecutionApproved(team, owner)
				if e != nil {
					return checkedTask{}, e
				}
				if !approved {
					return checkedTask{previous, "The team is awaiting human review. Submit the plan with TeamPlan and wait for approval before executing tasks."}, nil
				}
			}
			if teammate && previous != nil {
				if e := s.options.TaskStore.CheckTeammateExecution(list, *previous, in); e != nil {
					var blocked taskstore.ExecutionBlocked
					if errors.As(e, &blocked) {
						return checkedTask{previous, blocked.Error()}, nil
					}
					return checkedTask{}, e
				}
			}
			return checkedTask{task: previous}, nil
		})
		if e != nil {
			return nil, e
		}
		err = taskThen(scope, future, out, func(callback *cooperative.Scope, checked checkedTask) error {
			if checked.refusal != "" {
				return resolveTask(callback, out, taskRefusal(in.Get("taskId"), checked.refusal))
			}
			previous := checked.task
			if previous == nil {
				return resolveTask(callback, out, taskMissing(in.Get("taskId")))
			}
			if in.String("status") == "deleted" {
				removal, e := native(s, callback, func() (taskstore.Deletion, error) { return s.options.TaskStore.Delete(list, in.Get("taskId")) })
				if e != nil {
					return e
				}
				return taskThen(callback, removal, out, func(done *cooperative.Scope, deletion taskstore.Deletion) error {
					data := protocol.NewJSONObject(field("success", deletion.Deleted), protocol.JSONField{Name: "taskId", Value: in.Get("taskId")}, field("updatedFields", []string{}))
					if deletion.Deleted {
						data.Set("updatedFields", raw([]string{"deleted"}))
						data.Set("statusChange", object(protocol.JSONField{Name: "from", Value: previous.Get("status")}, field("to", "deleted")))
						data.Set("taskListMutationAt", quote(deletion.CommittedAt))
						data.Set("taskListMutationRevision", raw(*deletion.Revision))
					} else {
						data.Set("error", quote("Failed to delete task"))
					}
					return resolveTask(done, out, raw(data))
				})
			}
			if in.String("status") == "completed" && previous.String("status") != "completed" {
				if failure := s.taskHook(ctx, callback, "TaskCompleted", *previous, call); failure != nil {
					return resolveTask(callback, out, object(field("success", false), protocol.JSONField{Name: "taskId", Value: in.Get("taskId")}, field("updatedFields", []string{}), field("error", failure.Error())))
				}
			}
			type updatedTask struct {
				mutation        *taskstore.Mutation
				refusal         string
				assignmentError error
			}
			mutation, e := native(s, callback, func() (updatedTask, error) {
				var result *taskstore.Mutation
				var e error
				if teammate {
					result, e = s.options.TaskStore.UpdateForTeammate(list, in.Get("taskId"), in, member)
				} else {
					result, e = s.options.TaskStore.Update(list, in.Get("taskId"), in)
				}
				var blocked taskstore.ExecutionBlocked
				if errors.As(e, &blocked) {
					return updatedTask{refusal: blocked.Error()}, nil
				}
				if e != nil {
					return updatedTask{}, e
				}
				if result != nil && team != "" && result.Task.String("status") != "completed" && result.Task.String("owner") != "" && !jsonSame(result.Task.Get("owner"), result.Previous.Get("owner")) && s.options.Team.TaskAssigned != nil {
					if e := s.options.Team.TaskAssigned(team, result.Task); e != nil {
						return updatedTask{mutation: result, assignmentError: e}, nil
					}
				}
				return updatedTask{mutation: result}, nil
			})
			if e != nil {
				return e
			}
			return taskThen(callback, mutation, out, func(done *cooperative.Scope, updated updatedTask) error {
				if updated.assignmentError != nil && s.options.Team.TaskAssignmentError != nil {
					s.options.Team.TaskAssignmentError(done, updated.assignmentError)
				}
				if updated.refusal != "" {
					return resolveTask(done, out, taskRefusal(in.Get("taskId"), updated.refusal))
				}
				mutation := updated.mutation
				if mutation == nil {
					return resolveTask(done, out, taskMissing(in.Get("taskId")))
				}
				return resolveTask(done, out, taskUpdateResult(in.Get("taskId"), *mutation))
			})
		})
	default:
		return nil, capability("TaskV2 tool " + name)
	}
	if err != nil {
		_ = out.RejectAdministrative(err)
		return nil, err
	}
	return out, nil
}
func taskRefusal(id json.RawMessage, reason string) json.RawMessage {
	return object(field("success", false), protocol.JSONField{Name: "taskId", Value: id}, field("updatedFields", []string{}), field("error", reason))
}
func jsonSame(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	return bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b))
}
func taskUpdateResult(id json.RawMessage, mutation taskstore.Mutation) json.RawMessage {
	changed := []string{}
	for _, name := range []string{"subject", "description", "activeForm", "owner", "metadata", "status", "blocks", "blockedBy"} {
		if !jsonSame(mutation.Previous.Get(name), mutation.Task.Get(name)) {
			changed = append(changed, name)
		}
	}
	result := protocol.NewJSONObject(field("success", true), protocol.JSONField{Name: "taskId", Value: id}, field("updatedFields", changed))
	if !jsonSame(mutation.Previous.Get("status"), mutation.Task.Get("status")) {
		result.Set("statusChange", object(protocol.JSONField{Name: "from", Value: mutation.Previous.Get("status")}, protocol.JSONField{Name: "to", Value: mutation.Task.Get("status")}))
	}
	result.Set("verificationNudgeNeeded", raw(false))
	result.Set("taskListMutationAt", quote(mutation.CommittedAt))
	result.Set("taskListMutationRevision", raw(mutation.Revision))
	return raw(result)
}
func jsonTruthy(v json.RawMessage) bool {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || bytes.Equal(v, []byte("null")) || bytes.Equal(v, []byte("false")) {
		return false
	}
	if v[0] == '"' {
		u, _ := protocol.DecodeJavaScriptJSONString(v)
		return len(u) > 0
	}
	if v[0] == '{' || v[0] == '[' || v[0] == 't' {
		return true
	}
	n, err := strconv.ParseFloat(string(v), 64)
	return err == nil && n != 0
}
func taskListResult(snapshot taskstore.Snapshot) json.RawMessage {
	tasks := []protocol.JSONObject{}
	completed := map[string]bool{}
	for _, task := range snapshot.Tasks {
		var metadata protocol.JSONObject
		_ = metadata.UnmarshalJSON(task.Get("metadata"))
		if jsonTruthy(metadata.Get("_internal")) {
			continue
		}
		tasks = append(tasks, task)
		if task.String("status") == "completed" {
			completed[string(protocol.StringifyValidatedJavaScriptJSON(task.Get("id")))] = true
		}
	}
	result := []protocol.JSONObject{}
	for _, task := range tasks {
		var dependencies []json.RawMessage
		_ = json.Unmarshal(task.Get("blockedBy"), &dependencies)
		remaining := []json.RawMessage{}
		for _, id := range dependencies {
			if !completed[string(protocol.StringifyValidatedJavaScriptJSON(id))] {
				remaining = append(remaining, id)
			}
		}
		result = append(result, protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: task.Get("id")}, protocol.JSONField{Name: "subject", Value: task.Get("subject")}, protocol.JSONField{Name: "status", Value: task.Get("status")}, protocol.JSONField{Name: "owner", Value: task.Get("owner")}, field("blockedBy", remaining)))
	}
	return object(field("tasks", result), field("taskListSnapshotAt", snapshot.CapturedAt), field("taskListSnapshotRevision", snapshot.Revision))
}

// Mapping uses UTF-16 segments so lone surrogates remain unchanged in the
// actual tool_result, independent of native filename conversion.
type taskText []uint16

func (t *taskText) add(v string) {
	u, _ := protocol.DecodeJavaScriptJSONString(quote(v))
	*t = append(*t, u...)
}
func (t *taskText) value(v json.RawMessage) {
	u, _ := protocol.DecodeJavaScriptJSONString(v)
	*t = append(*t, u...)
}
func (t *taskText) ids(v json.RawMessage) {
	var ids []json.RawMessage
	_ = json.Unmarshal(v, &ids)
	for i, id := range ids {
		if i > 0 {
			t.add(", ")
		}
		t.add("#")
		t.value(id)
	}
}
func mapTask(name string, data protocol.JSONObject, id json.RawMessage, teammate ...bool) (protocol.JSONObject, error) {
	var content taskText
	switch name {
	case "TaskCreate":
		var task protocol.JSONObject
		_ = task.UnmarshalJSON(data.Get("task"))
		content.add("Task #")
		content.value(task.Get("id"))
		content.add(" created successfully: ")
		content.value(task.Get("subject"))
	case "TaskGet":
		if string(data.Get("task")) == "null" {
			content.add("Task not found")
			break
		}
		var task protocol.JSONObject
		_ = task.UnmarshalJSON(data.Get("task"))
		content.add("Task #")
		content.value(task.Get("id"))
		content.add(": ")
		content.value(task.Get("subject"))
		content.add("\nStatus: ")
		content.value(task.Get("status"))
		content.add("\nDescription: ")
		content.value(task.Get("description"))
		for _, pair := range []struct{ key, label string }{{"blockedBy", "\nBlocked by: "}, {"blocks", "\nBlocks: "}} {
			var ids []json.RawMessage
			_ = json.Unmarshal(task.Get(pair.key), &ids)
			if len(ids) > 0 {
				content.add(pair.label)
				content.ids(task.Get(pair.key))
			}
		}
	case "TaskList":
		var tasks []protocol.JSONObject
		_ = json.Unmarshal(data.Get("tasks"), &tasks)
		if len(tasks) == 0 {
			content.add("No tasks found")
		}
		for i, task := range tasks {
			if i > 0 {
				content.add("\n")
			}
			content.add("#")
			content.value(task.Get("id"))
			content.add(" [")
			content.value(task.Get("status"))
			content.add("] ")
			content.value(task.Get("subject"))
			if jsonTruthy(task.Get("owner")) {
				content.add(" (")
				content.value(task.Get("owner"))
				content.add(")")
			}
			var ids []json.RawMessage
			_ = json.Unmarshal(task.Get("blockedBy"), &ids)
			if len(ids) > 0 {
				content.add(" [blocked by ")
				content.ids(task.Get("blockedBy"))
				content.add("]")
			}
		}
	case "TaskUpdate":
		if !boolean(data, "success", false) {
			if jsonTruthy(data.Get("error")) {
				content.value(data.Get("error"))
			} else {
				content.add("Task #")
				content.value(data.Get("taskId"))
				content.add(" not found")
			}
			break
		}
		content.add("Updated task #")
		content.value(data.Get("taskId"))
		content.add(" ")
		var fields []json.RawMessage
		_ = json.Unmarshal(data.Get("updatedFields"), &fields)
		for i, field := range fields {
			if i > 0 {
				content.add(", ")
			}
			content.value(field)
		}
		if boolean(data, "verificationNudgeNeeded", false) {
			return protocol.JSONObject{}, capability("active TaskV2 verification agent")
		}
		var change protocol.JSONObject
		_ = change.UnmarshalJSON(data.Get("statusChange"))
		if len(teammate) > 0 && teammate[0] && change.String("to") == "completed" {
			content.add("\n\nTask completed. Call TaskList now to find your next available task or see if your work unblocked others.")
		}
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), protocol.JSONField{Name: "content", Value: protocol.QuoteJavaScriptUTF16(content)}), nil
}
