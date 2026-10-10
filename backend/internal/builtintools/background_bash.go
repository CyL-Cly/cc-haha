package builtintools

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

type backgroundShell struct {
	sandboxCleanup func()
	command        *exec.Cmd
	output         *os.File
	path           string
	cwdFilePath    string
	ctx            context.Context
	cancel         context.CancelCauseFunc
	done           chan struct{}
	finished       chan struct{}
	once           sync.Once
	waited         func() (int, error)
}

func taskIdentity(prefix byte) (string, error) {
	var bytes [8]byte
	if _, e := rand.Read(bytes[:]); e != nil {
		return "", e
	}
	out := make([]byte, 9)
	out[0] = prefix
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	for i, b := range bytes {
		out[i+1] = alphabet[b%36]
	}
	return string(out), nil
}
func taskTail(path string, max int64) (json.RawMessage, error) {
	f, e := os.Open(path)
	if e != nil {
		return quote(""), nil
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return quote(""), nil
	}
	offset := int64(0)
	if st.Size() > max {
		offset = st.Size() - max
	}
	if _, e = f.Seek(offset, io.SeekStart); e != nil {
		return quote(""), nil
	}
	body, e := io.ReadAll(io.LimitReader(f, max))
	if e != nil {
		return quote(""), nil
	}
	output := string(protocol.DecodeJavaScriptUTF8(body))
	if offset > 0 {
		output = fmt.Sprintf("[%dKB of earlier output omitted]\n", (offset+512)/1024) + output
	}
	return quote(output), nil
}
func (s *Service) startBackgroundBash(scope *cooperative.Scope, in protocol.JSONObject, toolID json.RawMessage) (json.RawMessage, error) {
	sandbox, e := s.selectBashSandbox(scope, in)
	if e != nil {
		return nil, e
	}
	if !s.HasBackgroundTaskBindings() {
		return nil, capability("Bash background task ownership and notifications")
	}
	b := s.options.BackgroundTasks
	command, e := text(in.Get("command"))
	if e != nil {
		return nil, e
	}
	id, e := taskIdentity('b')
	if e != nil {
		return nil, e
	}
	directory, e := s.backgroundOutputDirectory(scope)
	if e != nil {
		return nil, e
	}
	path := filepath.Join(directory, id+".output")
	environment, e := s.processEnvironment(scope)
	if e != nil {
		return nil, e
	}
	shell := environment["SHELL"]
	if shell == "" {
		shell = "/bin/bash"
	}
	p, e := executableInEnvironment(shell, environment)
	if e != nil {
		return nil, e
	}
	processContext, cancel := context.WithCancelCause(context.Background())
	execution, e := s.prepareBashExecution(processContext, scope, in.String("command"), id)
	if e != nil {
		cancel(e)
		return nil, e
	}
	prepareSandboxCommand(&execution, sandbox)
	prepared, e := native(s, scope, func() (*backgroundShell, error) {
		executionCommand, err := execution.resolveShellContext(processContext, shell)
		if err != nil {
			return nil, err
		}
		if b.CheckPath != nil {
			if e := b.CheckPath(path); e != nil {
				return nil, e
			}
		}
		if e := os.MkdirAll(b.OutputDir, 0777); e != nil {
			return nil, e
		}
		output, e := openTaskOutput(path)
		if e != nil {
			return nil, e
		}
		cmd := exec.CommandContext(processContext, p, "-l", "-c", executionCommand)
		cmd.Dir = execution.OriginalCwd
		keys := []string{}
		for key := range environment {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		cmd.Env = []string{}
		for _, key := range keys {
			cmd.Env = append(cmd.Env, key+"="+environment[key])
		}
		cmd.Stdout = output
		cmd.Stderr = output
		configureProcess(cmd)
		if e = cmd.Start(); e != nil {
			_ = output.Close()
			return nil, e
		}
		return &backgroundShell{command: cmd, output: output, path: path, cwdFilePath: execution.Path, sandboxCleanup: execution.cleanupSandbox, ctx: processContext, cancel: cancel, done: make(chan struct{}), finished: make(chan struct{})}, nil
	})
	if e != nil {
		cancel(e)
		return nil, e
	}
	process, e := prepared.Await(scope)
	if e != nil {
		cancel(e)
		return nil, e
	}
	lease, e := s.options.NativeBinding.Acquire(scope)
	if e != nil {
		cancel(e)
		_ = process.command.Wait()
		_ = process.output.Close()
		return nil, e
	}
	description := in.String("description")
	if description == "" {
		description = command
	}
	data := protocol.NewJSONObject(field("id", id), field("type", "local_bash"), field("status", "running"), field("description", description), field("startTime", s.options.NowMillis()), field("outputFile", path), field("outputOffset", 0), field("notified", false), field("command", command), field("completionStatusSentInAttachment", false), field("lastReportedTotalLines", 0), field("isBackgrounded", true))
	if toolID != nil {
		data.Set("toolUseId", toolID)
	}
	if b.AgentID != "" {
		data.Set("agentId", quote(b.AgentID))
	}
	task := &sessioncontroller.BackgroundTask{Data: data, Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return taskTail(path, 8*1024*1024) }, ReadActiveOutput: func(context.Context) (json.RawMessage, error) {
		return taskHead(path, s.options.Environment["BASH_MAX_OUTPUT_LENGTH"])
	}, Join: func(ctx context.Context) error {
		select {
		case <-process.finished:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}}
	if e = b.Register(scope, task); e != nil {
		cancel(e)
		_ = process.command.Wait()
		_ = process.output.Close()
		lease.DeliveryFinished()
		return nil, e
	}
	go s.waitBackgroundShell(process, id, description, toolID, lease)
	event := protocol.NewJSONObject(field("type", "system"), field("subtype", "task_started"), field("task_id", id), field("description", description), field("task_type", "local_bash"))
	if toolID != nil {
		event.Set("tool_use_id", toolID)
	}
	if b.AgentID == "" {
		if e = b.Emit(scope, event); e != nil {
			cancel(e)
			return nil, e
		}
	}
	return object(field("stdout", ""), field("stderr", ""), field("interrupted", false), field("isImage", false), field("backgroundTaskId", id), field("noOutputExpected", nativeBashSilent(command, s.options.Platform))), nil
}

func taskHead(path, limit string) (json.RawMessage, error) {
	maximum := boundedTaskOutputLimit(limit, 30000, 150000)
	f, e := os.Open(path)
	if e == nil {
		defer f.Close()
		var data []byte
		data, e = io.ReadAll(io.LimitReader(f, int64(maximum)))
		if e == nil {
			return quote(string(protocol.DecodeJavaScriptUTF8(data))), nil
		}
	}
	code := "unknown"
	switch {
	case os.IsNotExist(e):
		code = "ENOENT"
	case os.IsPermission(e):
		code = "EACCES"
	}
	return quote("<bash output unavailable: output file " + path + " could not be read (" + code + "). This usually means another Claude Code process in the same project deleted it during startup cleanup.>"), nil
}
func (s *Service) waitBackgroundShell(process *backgroundShell, id, description string, toolID json.RawMessage, lease *ownerasync.NativeReference) {
	// Native process output is one real interleaved file. The watchdog observes
	// that file and cancels its entire process group at Source's 5 GB disk cap.
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		timer := time.NewTicker(5 * time.Second)
		defer timer.Stop()
		lastSize := int64(0)
		lastGrowth := time.Now()
		stallNotified := false
		for {
			select {
			case <-process.done:
				return
			case <-timer.C:
				st, e := os.Stat(process.path)
				if e != nil {
					continue
				}
				if st.Size() > 5*1024*1024*1024 {
					process.cancel(errors.New("Task output exceeded 5GB disk cap"))
					continue
				}
				if st.Size() > lastSize {
					lastSize = st.Size()
					lastGrowth = time.Now()
					continue
				}
				if !stallNotified && time.Since(lastGrowth) >= 45*time.Second {
					tail, _ := taskTail(process.path, 1024)
					content, _ := text(tail)
					if !looksLikeTaskPrompt(content) {
						lastGrowth = time.Now()
						continue
					}
					stallNotified = true
					message := shellNotification(id, description, "", nil, toolID, process.path) + "\nLast output:\n" + strings.TrimRightFunc(content, bashSpace) + "\n\nThe command is likely blocked on an interactive prompt. Kill this task and re-run with piped input (e.g., `echo y | command`) or a non-interactive flag if one exists."
					s.deliverShellProgress(id, message)
				}
			}
		}
	}()
	var e error
	code := 0
	if process.waited != nil {
		code, e = process.waited()
	} else {
		e = process.command.Wait()
		if process.sandboxCleanup != nil {
			process.sandboxCleanup()
		}
	}
	if e != nil {
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			code = exit.ExitCode()
			if code < 0 {
				code = 137
			}
		} else {
			code = 1
		}
	}
	_ = process.output.Close()
	if process.cwdFilePath != "" {
		_ = os.Remove(process.cwdFilePath)
	}
	close(process.done)
	<-watchdogDone
	receipt, failure := s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		b := s.options.BackgroundTasks
		task, e := b.Lookup(scope, id)
		if e != nil {
			return e
		}
		if task == nil {
			return nil
		}
		if task.Data.String("status") == "killed" {
			return nil
		}
		status := "completed"
		if code != 0 {
			status = "failed"
		}
		if e = b.Update(scope, id, func(data protocol.JSONObject) (protocol.JSONObject, error) {
			data.Set("status", quote(status))
			data.Set("result", object(field("code", code), field("interrupted", process.ctx.Err() != nil)))
			data.Set("endTime", raw(s.options.NowMillis()))
			return data, nil
		}); e != nil {
			return e
		}
		if boolean(task.Data, "notified", false) {
			return nil
		}
		if e = b.MarkNotified(scope, id); e != nil {
			return e
		}
		if e = b.Notify(scope, id, shellNotification(id, description, status, &code, toolID, process.path)); e != nil {
			return e
		}
		if b.AgentID == "" {
			if b.RootNotificationsUseQueue != nil && b.RootNotificationsUseQueue(scope) {
				return nil
			}
			event := protocol.NewJSONObject(field("type", "system"), field("subtype", "task_notification"), field("task_id", id), field("status", status), field("output_file", process.path), field("summary", shellSummary(description, status, &code)))
			if toolID != nil {
				event.Set("tool_use_id", toolID)
			}
			return b.Emit(scope, event)
		}
		return nil
	})
	if failure == nil {
		failure = receipt.Wait()
	}
	lease.DeliveryFinished()
	close(process.finished)
	_ = failure
}
func (s *Service) deliverShellProgress(id, message string) {
	_, _ = s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		task, e := s.options.BackgroundTasks.Lookup(scope, id)
		if e != nil || task == nil || task.Data.String("status") != "running" {
			return e
		}
		return s.options.BackgroundTasks.Notify(scope, id, message)
	})
}
func shellSummary(description, status string, code *int) string {
	base := "Background command \"" + description + "\" "
	switch status {
	case "completed":
		base += "completed"
		if code != nil {
			base += fmt.Sprintf(" (exit code %d)", *code)
		}
	case "failed":
		base += "failed"
		if code != nil {
			base += fmt.Sprintf(" with exit code %d", *code)
		}
	case "killed":
		base += "was stopped"
	default:
		base += "appears to be waiting for interactive input"
	}
	return base
}
func shellNotification(id, description, status string, code *int, toolID json.RawMessage, path string) string {
	lines := []string{"<task-notification>", "<task-id>" + id + "</task-id>"}
	if toolID != nil {
		value, _ := text(toolID)
		lines = append(lines, "<tool-use-id>"+value+"</tool-use-id>")
	}
	lines = append(lines, "<output-file>"+path+"</output-file>")
	if status != "" {
		lines = append(lines, "<status>"+status+"</status>")
	}
	escape := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	lines = append(lines, "<summary>"+escape.Replace(shellSummary(description, status, code))+"</summary>", "</task-notification>")
	return strings.Join(lines, "\n")
}

var taskPromptPattern = regexp.MustCompile(`(?i)\(y/n\)|\[y/n\]|\(yes/no\)|\b(?:Do you|Would you|Shall I|Are you sure|Ready to)\b.*\? *$|Press (?:any key|Enter)|Continue\?|Overwrite\?`)

func looksLikeTaskPrompt(tail string) bool {
	line := strings.TrimRightFunc(tail, bashSpace)
	if i := strings.LastIndexByte(line, '\n'); i >= 0 {
		line = line[i+1:]
	}
	return taskPromptPattern.MatchString(line)
}
func (s *Service) StartBackgroundCleanup(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if !s.HasBackgroundTaskBindings() {
		f, _ := ownerasync.NewOwnerFuture[struct{}](s.options.Owner)
		return f, f.Resolve(scope, struct{}{})
	}
	tasks, e := s.options.BackgroundTasks.List(scope)
	if e != nil {
		return nil, e
	}
	joiners := []func(context.Context) error{}
	for _, data := range tasks {
		// In-process teammates register their own lifecycle cancellation and
		// join callback; the original TaskStop intentionally rejects this type.
		if data.String("type") == "in_process_teammate" {
			continue
		}
		task, e := s.options.BackgroundTasks.Lookup(scope, data.String("id"))
		if e != nil {
			return nil, e
		}
		if task == nil {
			continue
		}
		if data.String("status") == "running" {
			if _, e = s.stopBackgroundTask(scope, data.String("id")); e != nil {
				return nil, e
			}
		}
		joiners = append(joiners, task.Join)
	}
	return native(s, scope, func() (struct{}, error) {
		for _, join := range joiners {
			if e := join(context.Background()); e != nil {
				return struct{}{}, e
			}
		}
		return struct{}{}, nil
	})
}
func (s *Service) callBash(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
	view, e := s.bashContextView(scopeFor(c.Opaque), c.Opaque)
	if e != nil {
		return queryloop.ToolCallResult{}, e
	}
	return view.callBashOnContext(ctx, in, c)
}

func (s *Service) callBashOnContext(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
	scope := scopeFor(c.Opaque)
	if !boolean(in, "run_in_background", false) || skillSimple(s.options.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]) {
		if s.HasBackgroundTaskBindings() && !skillSimple(s.options.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]) {
			data, e := s.foregroundBash(ctx, in, c)
			return queryloop.ToolCallResult{Data: data}, e
		}
		data, e := s.bash(ctx, in, scope)
		return queryloop.ToolCallResult{Data: data}, e
	}
	if e := s.check(scope); e != nil {
		return queryloop.ToolCallResult{}, e
	}
	data, e := s.startBackgroundBash(scope, in, c.ToolUseID)
	return queryloop.ToolCallResult{Data: data}, e
}

func (s *Service) mapBashResult(data, id json.RawMessage) (protocol.JSONObject, error) {
	result := inputObject(data)
	taskID := result.String("backgroundTaskId")
	if taskID == "" {
		return MapResult("Bash", data, id)
	}
	if !s.HasBackgroundTaskBindings() {
		return protocol.JSONObject{}, capability("actual background output path")
	}
	result.Delete("backgroundTaskId")
	mapped, e := MapResult("Bash", raw(result), id)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	content := mapped.String("content")
	if content != "" {
		content += "\n"
	}
	if boolean(result, "backgroundedByUser", false) {
		content += "Command was manually backgrounded by user with ID: " + taskID + ". Output is being written to: " + filepath.Join(s.options.BackgroundTasks.OutputDir, taskID+".output")
	} else {
		content += "Command running in background with ID: " + taskID + ". Output is being written to: " + filepath.Join(s.options.BackgroundTasks.OutputDir, taskID+".output")
	}
	mapped.Set("content", quote(content))
	return mapped, nil
}
