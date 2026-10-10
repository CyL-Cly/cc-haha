package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// These callbacks reach the one Controller.AppState.tasks producer. No tool,
// host, or native process keeps a competing registry of task state.
type BackgroundTaskBindings struct {
	Register                  func(*cooperative.Scope, *sessioncontroller.BackgroundTask) error
	Lookup                    func(*cooperative.Scope, string) (*sessioncontroller.BackgroundTask, error)
	List                      func(*cooperative.Scope) ([]protocol.JSONObject, error)
	Update                    func(*cooperative.Scope, string, func(protocol.JSONObject) (protocol.JSONObject, error)) error
	MarkNotified              func(*cooperative.Scope, string) error
	Stop                      func(*cooperative.Scope, string, float64) (protocol.JSONObject, error)
	Delete                    func(*cooperative.Scope, string) error
	OutputDir                 string
	OutputDirectory           func(*cooperative.Scope) (string, error)
	AgentID                   string
	Emit                      func(*cooperative.Scope, protocol.JSONObject) error
	Notify                    func(*cooperative.Scope, string, string) error
	NotifyForAgent            func(*cooperative.Scope, string, string) error
	RootNotificationsUseQueue func(*cooperative.Scope) bool
	RegisterName              func(*cooperative.Scope, string, string) error
	ResolveName               func(*cooperative.Scope, string) (string, error)
	CheckPath                 func(string) error
}

func backgroundTaskTool(name string) bool { return name == "TaskOutput" || name == "TaskStop" }
func (s *Service) HasBackgroundTaskBindings() bool {
	b := s.options.BackgroundTasks
	return b != nil && b.Register != nil && b.Lookup != nil && b.List != nil && b.Update != nil && b.MarkNotified != nil && b.Stop != nil && (b.OutputDir != "" || b.OutputDirectory != nil) && b.Emit != nil && b.Notify != nil
}

func (s *Service) backgroundOutputDirectory(scope *cooperative.Scope) (string, error) {
	b := s.options.BackgroundTasks
	if b == nil {
		return "", capability("actual task output directory")
	}
	if b.OutputDir != "" {
		return b.OutputDir, nil
	}
	if b.OutputDirectory == nil {
		return "", capability("actual task output directory")
	}
	directory, e := b.OutputDirectory(scope)
	if e != nil {
		return "", e
	}
	b.OutputDir = directory
	return directory, nil
}
func backgroundTaskID(in protocol.JSONObject) string {
	if in.Get("task_id") != nil {
		return in.String("task_id")
	}
	return in.String("shell_id")
}
func (s *Service) validateBackgroundTask(scope *cooperative.Scope, name string, in protocol.JSONObject) error {
	if !s.HasBackgroundTaskBindings() {
		return capability("actual AppState task producer")
	}
	id := backgroundTaskID(in)
	if id == "" {
		if name == "TaskOutput" {
			return errors.New("Task ID is required")
		}
		return errors.New("Missing required parameter: task_id")
	}
	task, e := s.options.BackgroundTasks.Lookup(scope, id)
	if e != nil {
		return e
	}
	if task == nil {
		return fmt.Errorf("No task found with ID: %s", id)
	}
	if name == "TaskStop" && task.Data.String("status") != "running" {
		return fmt.Errorf("Task %s is not running (status: %s)", id, task.Data.String("status"))
	}
	return nil
}
func (s *Service) stopBackgroundTask(scope *cooperative.Scope, id string) (protocol.JSONObject, error) {
	b := s.options.BackgroundTasks
	task, e := b.Lookup(scope, id)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	result, e := b.Stop(scope, id, s.options.NowMillis())
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if task != nil && task.Data.String("type") == "local_bash" && task.Data.Get("agentId") == nil && !boolean(task.Data, "notified", false) {
		event := protocol.NewJSONObject(field("type", "system"), field("subtype", "task_notification"), field("task_id", id), field("status", "stopped"), field("output_file", ""), field("summary", task.Data.String("description")))
		if value := task.Data.Get("toolUseId"); value != nil {
			event.Set("tool_use_id", value)
		}
		if e = b.Emit(scope, event); e != nil {
			return protocol.JSONObject{}, e
		}
	}
	return result, nil
}

// StopBackgroundTaskFromControl preserves the original UI race semantics:
// unknown handles are already gone, while model tools still reject them.
func (s *Service) StopBackgroundTaskFromControl(scope *cooperative.Scope, id string) (protocol.JSONObject, error) {
	if e := s.check(scope); e != nil {
		return protocol.JSONObject{}, e
	}
	if !s.HasBackgroundTaskBindings() {
		return protocol.JSONObject{}, capability("actual AppState task producer")
	}
	_, e := s.stopBackgroundTask(scope, id)
	if e == nil {
		return protocol.NewJSONObject(field("ok", true), field("alreadyGone", false)), nil
	}
	var stop *sessioncontroller.StopBackgroundTaskError
	if errors.As(e, &stop) && stop.Code == "not_found" {
		return protocol.NewJSONObject(field("ok", true), field("alreadyGone", true)), nil
	}
	return protocol.NewJSONObject(field("ok", false), field("message", e.Error())), nil
}
func (s *Service) callBackgroundTask(ctx context.Context, scope *cooperative.Scope, name string, in protocol.JSONObject, onProgress func(json.RawMessage, protocol.JSONObject) error) (json.RawMessage, error) {
	if !s.HasBackgroundTaskBindings() {
		return nil, capability("actual AppState task producer")
	}
	id := backgroundTaskID(in)
	if _, e := s.backgroundOutputDirectory(scope); e != nil {
		return nil, e
	}
	if name == "TaskStop" {
		if id == "" {
			return nil, errors.New("Missing required parameter: task_id")
		}
		result, e := s.stopBackgroundTask(scope, id)
		if e != nil {
			return nil, e
		}
		command := result.String("command")
		out := protocol.NewJSONObject(field("message", "Successfully stopped task: "+id+" ("+command+")"), field("task_id", id), field("task_type", result.String("taskType")))
		if result.Get("command") != nil {
			out.Set("command", result.Get("command"))
		}
		return raw(out), nil
	}
	task, e := s.options.BackgroundTasks.Lookup(scope, id)
	if e != nil {
		return nil, e
	}
	if task == nil {
		return nil, fmt.Errorf("No task found with ID: %s", id)
	}
	retrieval := "not_ready"
	if boolean(in, "block", true) {
		if onProgress != nil {
			p := protocol.NewJSONObject(field("type", "waiting_for_task"), field("taskDescription", task.Data.String("description")), field("taskType", task.Data.String("type")))
			if e = onProgress(quote(fmt.Sprintf("task-output-waiting-%s", strconv.FormatFloat(s.options.NowMillis(), 'f', -1, 64))), p); e != nil {
				return nil, e
			}
		}
		timeout := 30000.0
		if in.Get("timeout") != nil {
			_ = json.Unmarshal(in.Get("timeout"), &timeout)
		}
		start := s.options.NowMillis()
		for s.options.NowMillis()-start < timeout {
			if e = context.Cause(ctx); e != nil {
				return nil, &toolerrorsAbort{}
			}
			task, e = s.options.BackgroundTasks.Lookup(scope, id)
			if e != nil {
				return nil, e
			}
			if task == nil || task.Data.String("status") != "running" && task.Data.String("status") != "pending" {
				break
			}
			timer, e := native(s, scope, func() (struct{}, error) { time.Sleep(100 * time.Millisecond); return struct{}{}, nil })
			if e != nil {
				return nil, e
			}
			if _, e = timer.Await(scope); e != nil {
				return nil, e
			}
		}
		task, e = s.options.BackgroundTasks.Lookup(scope, id)
		if e != nil {
			return nil, e
		}
		retrieval = "timeout"
	}
	if task == nil {
		return object(field("retrieval_status", retrieval), field("task", nil)), nil
	}
	if task.Data.String("status") != "running" && task.Data.String("status") != "pending" {
		retrieval = "success"
		if e = s.options.BackgroundTasks.MarkNotified(scope, id); e != nil {
			return nil, e
		}
	}
	reader := task.ReadOutput
	if task.Data.String("type") == "local_bash" && task.Data.String("status") == "running" && task.ReadActiveOutput != nil {
		reader = task.ReadActiveOutput
	}
	f, e := native(s, scope, func() (json.RawMessage, error) { return reader(ctx) })
	if e != nil {
		return nil, e
	}
	output, e := f.Await(scope)
	if e != nil {
		return nil, e
	}
	out := protocol.NewJSONObject(field("task_id", id), protocol.JSONField{Name: "task_type", Value: task.Data.Get("type")}, protocol.JSONField{Name: "status", Value: task.Data.Get("status")}, protocol.JSONField{Name: "description", Value: task.Data.Get("description")}, protocol.JSONField{Name: "output", Value: output})
	switch task.Data.String("type") {
	case "local_bash":
		result := inputObject(task.Data.Get("result"))
		code := result.Get("code")
		if code == nil {
			code = raw(nil)
		}
		out.Set("exitCode", code)
	case "local_agent":
		result := inputObject(task.Data.Get("result"))
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(result.Get("content"), &blocks)
		parts := []string{}
		for _, block := range blocks {
			if block.String("type") == "text" {
				parts = append(parts, block.String("text"))
			}
		}
		clean := strings.Join(parts, "\n")
		if clean != "" {
			output = quote(clean)
			out.Set("output", output)
		}
		if task.Data.Get("prompt") != nil {
			out.Set("prompt", task.Data.Get("prompt"))
		}
		out.Set("result", output)
		if task.Data.Get("error") != nil {
			out.Set("error", task.Data.Get("error"))
		}
	case "remote_agent":
		if task.Data.Get("command") != nil {
			out.Set("prompt", task.Data.Get("command"))
		}
	}
	return object(field("retrieval_status", retrieval), field("task", out)), nil
}

type toolerrorsAbort struct{}

func (*toolerrorsAbort) Error() string { return "" }
func inputObject(v json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = json.Unmarshal(v, &o)
	return o
}
func (s *Service) startBackgroundTask(ctx context.Context, name string, in protocol.JSONObject, c queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(c.Opaque)
	if e := s.check(scope); e != nil {
		return nil, e
	}
	future, e := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](s.options.Owner)
	if e != nil {
		return nil, e
	}
	// The owner continuation is the sole consumer; each wait yields to physical
	// completion on the same owner rather than reading mutable state in a goroutine.
	data, failure := s.callBackgroundTask(ctx, scope, name, in, c.OnProgress)
	if failure != nil {
		e = future.Reject(scope, failure)
	} else {
		e = future.Resolve(scope, queryloop.ToolCallResult{Data: data})
	}
	return future, e
}
func (s *Service) mapBackgroundTask(name string, data, id json.RawMessage) (protocol.JSONObject, error) {
	if name == "TaskStop" {
		encoded, e := protocol.ParseAndStringifyJavaScriptJSON(data)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		return textResult(id, string(encoded)), nil
	}
	result := inputObject(data)
	parts := []string{"<retrieval_status>" + result.String("retrieval_status") + "</retrieval_status>"}
	outputIndex := -1
	var outputUnits []uint16
	task := inputObject(result.Get("task"))
	if task.IsObject() {
		parts = append(parts, "<task_id>"+task.String("task_id")+"</task_id>", "<task_type>"+task.String("task_type")+"</task_type>", "<status>"+task.String("status")+"</status>")
		if code := task.Get("exitCode"); code != nil && string(code) != "null" {
			parts = append(parts, "<exit_code>"+string(code)+"</exit_code>")
		}
		units, e := protocol.DecodeJavaScriptJSONString(task.Get("output"))
		if e != nil {
			return protocol.JSONObject{}, e
		}
		if strings.TrimFunc(string(utf16.Decode(units)), bashSpace) != "" {
			max := taskOutputLimit(s.options.Environment["TASK_MAX_OUTPUT_LENGTH"])
			if len(units) > max {
				header := utf16.Encode([]rune("[Truncated. Full output: " + s.options.BackgroundTasks.OutputDir + "/" + task.String("task_id") + ".output]\n\n"))
				available := max - len(header)
				start := len(units) - available
				if available < 0 {
					start = -available
				}
				if start > len(units) {
					start = len(units)
				}
				units = append(header, units[start:]...)
			}
			for len(units) > 0 && bashSpace(rune(units[len(units)-1])) {
				units = units[:len(units)-1]
			}
			outputIndex = len(parts)
			outputUnits = append(utf16.Encode([]rune("<output>\n")), units...)
			outputUnits = append(outputUnits, utf16.Encode([]rune("\n</output>"))...)
			parts = append(parts, "")
		}
		if task.String("error") != "" {
			parts = append(parts, "<error>"+task.String("error")+"</error>")
		}
	}
	content := []uint16{}
	for i, part := range parts {
		if i > 0 {
			content = append(content, '\n', '\n')
		}
		if i == outputIndex {
			content = append(content, outputUnits...)
		} else {
			content = append(content, utf16.Encode([]rune(part))...)
		}
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), protocol.JSONField{Name: "content", Value: protocol.QuoteJavaScriptUTF16(content)}), nil
}
func taskOutputLimit(value string) int { return boundedTaskOutputLimit(value, 32000, 160000) }
func boundedTaskOutputLimit(value string, fallback, upper int) int {
	value = strings.TrimLeftFunc(value, bashSpace)
	sign := 1
	if strings.HasPrefix(value, "-") {
		sign = -1
		value = value[1:]
	} else if strings.HasPrefix(value, "+") {
		value = value[1:]
	}
	n := 0
	for n < len(value) && value[n] >= '0' && value[n] <= '9' {
		n++
	}
	if n == 0 {
		return fallback
	}
	v, e := strconv.ParseFloat(value[:n], 64)
	if sign < 0 || v <= 0 {
		return fallback
	}
	if v > float64(upper) {
		return upper
	}
	if e != nil {
		return fallback
	}
	return int(v)
}
