package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func foregroundTaskRead(path string) (json.RawMessage, error) {
	f, e := os.Open(path)
	if e != nil {
		return quoteMain(""), nil
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return quoteMain(""), nil
	}
	offset := int64(0)
	if st.Size() > 8*1024*1024 {
		offset = st.Size() - 8*1024*1024
	}
	_, _ = f.Seek(offset, io.SeekStart)
	data, e := io.ReadAll(io.LimitReader(f, 8*1024*1024))
	if e != nil {
		return quoteMain(""), nil
	}
	out := string(protocol.DecodeJavaScriptUTF8(data))
	if offset > 0 {
		out = "[" + strconv.FormatInt((offset+512)/1024, 10) + "KB of earlier output omitted]\n" + out
	}
	return quoteMain(out), nil
}
func (m *MainPrint) beginForegroundAgentTask(scope *cooperative.Scope, agentID, path string, request builtintools.AgentRequest, cancel context.CancelCauseFunc, done <-chan struct{}) error {
	return m.beginNativeAgentTask(scope, agentID, path, request, cancel, done, false)
}
func (m *MainPrint) beginNativeAgentTask(scope *cooperative.Scope, agentID, path string, request builtintools.AgentRequest, cancel context.CancelCauseFunc, done <-chan struct{}, background bool) error {
	registration, e := m.prepareNativeAgentTask(scope, agentID, path, request, cancel, done, background)
	if e == nil {
		registration.Commit()
	}
	return e
}

func (m *MainPrint) prepareNativeAgentTask(scope *cooperative.Scope, agentID, path string, request builtintools.AgentRequest, cancel context.CancelCauseFunc, done <-chan struct{}, background bool) (*sessioncontroller.BackgroundTaskRegistration, error) {
	b := m.nativeBackgroundBindings()
	data := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoteMain(agentID)}, protocol.JSONField{Name: "type", Value: quoteMain("local_agent")}, protocol.JSONField{Name: "status", Value: quoteMain("running")}, protocol.JSONField{Name: "description", Value: quoteMain(request.Description)}, protocol.JSONField{Name: "startTime", Value: rawMain(m.options.Native.NowMillis())}, protocol.JSONField{Name: "outputOffset", Value: rawMain(0)}, protocol.JSONField{Name: "notified", Value: rawMain(false)}, protocol.JSONField{Name: "agentId", Value: quoteMain(agentID)}, protocol.JSONField{Name: "prompt", Value: quoteMain(request.Prompt)}, protocol.JSONField{Name: "agentType", Value: quoteMain(request.AgentType)}, protocol.JSONField{Name: "retrieved", Value: rawMain(false)}, protocol.JSONField{Name: "lastReportedToolCount", Value: rawMain(0)}, protocol.JSONField{Name: "lastReportedTokenCount", Value: rawMain(0)}, protocol.JSONField{Name: "isBackgrounded", Value: rawMain(false)}, protocol.JSONField{Name: "pendingMessages", Value: rawMain([]string{})}, protocol.JSONField{Name: "retain", Value: rawMain(false)}, protocol.JSONField{Name: "diskLoaded", Value: rawMain(false)})
	if request.SelectedAgent.IsObject() {
		data.Set("selectedAgent", rawMain(request.SelectedAgent))
	}
	data.Set("isBackgrounded", rawMain(background))
	directory, e := m.nativeTaskOutputDirectory(scope)
	if e != nil {
		return nil, e
	}
	output := filepath.Join(directory, agentID+".output")
	if e := os.MkdirAll(directory, 0777); e != nil {
		return nil, e
	}
	if e := os.Symlink(path, output); e != nil {
		if !os.IsNotExist(e) {
			_ = os.Remove(output)
			e = os.Symlink(path, output)
		}
		if e != nil {
			output = path
		}
	}
	data.Set("outputFile", quoteMain(output))
	if request.ToolUseID != nil {
		data.Set("toolUseId", request.ToolUseID)
	}
	if m.options.AgentID != "" {
		data.Set("ownerAgentId", quoteMain(m.options.AgentID))
	}
	if request.OwnerAgentID != "" {
		data.Set("ownerAgentId", quoteMain(request.OwnerAgentID))
	}
	entry := &sessioncontroller.BackgroundTask{Data: data, Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return foregroundTaskRead(output) }, ReadDelta: func(_ context.Context, offset int64) (bool, int64, error) {
		f, e := os.Open(output)
		if e != nil {
			return false, offset, nil
		}
		defer f.Close()
		_, _ = f.Seek(offset, io.SeekStart)
		data, e := io.ReadAll(io.LimitReader(f, 8*1024*1024))
		return e == nil && len(data) > 0, offset + int64(len(data)), nil
	}, Join: func(ctx context.Context) error {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}}
	if b.RegisterAgent == nil {
		return nil, mainCap("Agent task registration compensation")
	}
	name := ""
	if background {
		name = request.Name
	}
	registration, e := b.RegisterAgent(scope, entry, name)
	if e != nil {
		return nil, e
	}
	if registration.Replaced {
		return registration, nil
	}
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_started")}, protocol.JSONField{Name: "task_id", Value: quoteMain(agentID)}, protocol.JSONField{Name: "description", Value: quoteMain(request.Description)}, protocol.JSONField{Name: "task_type", Value: quoteMain("local_agent")}, protocol.JSONField{Name: "prompt", Value: quoteMain(request.Prompt)})
	if request.ToolUseID != nil {
		event.Set("tool_use_id", request.ToolUseID)
	}
	if m.options.AgentID != "" {
		event.Set("owner_agent_id", quoteMain(m.options.AgentID))
	}
	if e = b.Emit(scope, event); e != nil {
		return nil, errors.Join(e, registration.Rollback(scope))
	}
	return registration, nil
}
func (m *MainPrint) endForegroundAgentTask(scope *cooperative.Scope, agentID string, request builtintools.AgentRequest, start float64, cause error, progress *foregroundAgentProgress) error {
	if e := m.nativeBackgroundBindings().Delete(scope, agentID); e != nil {
		return e
	}
	status := "completed"
	if cause != nil {
		status = "failed"
		if errors.Is(cause, context.Canceled) {
			status = "stopped"
		}
	}
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_notification")}, protocol.JSONField{Name: "task_id", Value: quoteMain(agentID)}, protocol.JSONField{Name: "status", Value: quoteMain(status)}, protocol.JSONField{Name: "output_file", Value: quoteMain("")}, protocol.JSONField{Name: "summary", Value: quoteMain(request.Description)}, protocol.JSONField{Name: "usage", Value: rawMain(map[string]any{"total_tokens": progress.input + progress.output, "tool_uses": progress.tools, "duration_ms": m.options.Native.NowMillis() - start})})
	if request.ToolUseID != nil {
		event.Set("tool_use_id", request.ToolUseID)
	}
	if m.options.AgentID != "" {
		event.Set("owner_agent_id", quoteMain(m.options.AgentID))
	}
	return m.nativeBackgroundBindings().Emit(scope, event)
}
func (m *MainPrint) killAgentOwnedShells(scope *cooperative.Scope, agentID string) error {
	b := m.nativeBackgroundBindings()
	tasks, e := b.List(scope)
	if e != nil {
		return e
	}
	for _, task := range tasks {
		if task.String("type") == "local_bash" && task.String("agentId") == agentID && task.String("status") == "running" {
			if _, e = b.Stop(scope, task.String("id"), m.options.Native.NowMillis()); e != nil {
				return e
			}
		}
	}
	if d := m.options.DetachedAgent; d != nil {
		return d.pruneQueued(m, scope, agentID)
	}
	return m.Controller.PruneAgentTaskNotificationsOnOwner(scope, agentID)
}

type foregroundAgentProgress struct {
	input, output, tools float64
	activity             func(string, protocol.JSONObject) json.RawMessage
	lastActivity         json.RawMessage
	recent               []protocol.JSONObject
	classification       func(string, protocol.JSONObject) (bool, bool, error)
	failure              error
}

func (p *foregroundAgentProgress) observe(data json.RawMessage) string {
	var envelope protocol.JSONObject
	if json.Unmarshal(data, &envelope) != nil || envelope.String("type") != "assistant" {
		return ""
	}
	api, _ := envelope.Object("message")
	usage, _ := api.Object("usage")
	p.input = 0
	for _, key := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
		var n float64
		_ = json.Unmarshal(usage.Get(key), &n)
		p.input += n
	}
	var out float64
	_ = json.Unmarshal(usage.Get("output_tokens"), &out)
	p.output += out
	var blocks []protocol.JSONObject
	_ = json.Unmarshal(api.Get("content"), &blocks)
	lastTool := ""
	for _, block := range blocks {
		if block.String("type") == "tool_use" {
			p.tools++
			lastTool = block.String("name")
			if lastTool != "StructuredOutput" {
				input, _ := block.Object("input")
				activity := protocol.NewJSONObject(protocol.JSONField{Name: "toolName", Value: block.Get("name")}, protocol.JSONField{Name: "input", Value: block.Get("input")})
				if p.activity != nil {
					p.lastActivity = p.activity(lastTool, input)
					if p.lastActivity != nil {
						activity.Set("activityDescription", p.lastActivity)
					}
				}
				if p.classification != nil {
					search, read, err := p.classification(lastTool, input)
					if err != nil {
						p.failure = err
					}
					activity.Set("isSearch", rawMain(search))
					activity.Set("isRead", rawMain(read))
				}
				p.recent = append(p.recent, activity)
				if len(p.recent) > 5 {
					p.recent = p.recent[len(p.recent)-5:]
				}
			}
		}
	}
	return lastTool
}

func (m *MainPrint) emitForegroundAgentProgress(scope *cooperative.Scope, agentID string, request builtintools.AgentRequest, start float64, progress *foregroundAgentProgress, lastTool string) error {
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_progress")}, protocol.JSONField{Name: "task_id", Value: quoteMain(agentID)}, protocol.JSONField{Name: "description", Value: quoteMain(request.Description)}, protocol.JSONField{Name: "usage", Value: rawMain(map[string]any{"total_tokens": progress.input + progress.output, "tool_uses": progress.tools, "duration_ms": m.options.Native.NowMillis() - start})}, protocol.JSONField{Name: "last_tool_name", Value: quoteMain(lastTool)})
	if progress.lastActivity != nil {
		event.Set("description", progress.lastActivity)
	}
	if request.ToolUseID != nil {
		event.Set("tool_use_id", request.ToolUseID)
	}
	if m.options.AgentID != "" {
		event.Set("owner_agent_id", quoteMain(m.options.AgentID))
	}
	return m.nativeBackgroundBindings().Emit(scope, event)
}

func (m *MainPrint) nativeAgentProgress(request builtintools.AgentRequest, cwd string) *foregroundAgentProgress {
	home, taskDirectory := m.options.Native.Home, m.taskOutputDirectory
	available := map[string]bool{}
	for _, name := range request.ParentTools {
		available[name] = true
	}
	return &foregroundAgentProgress{classification: builtintools.NativeActivityClassification, activity: func(name string, input protocol.JSONObject) json.RawMessage {
		if !available[name] {
			return nil
		}
		// These getters only project original tool metadata. They do not run
		// tools or read task state on a worker.
		value, known, e := builtintools.NativeActivityDescription(name, input, home, cwd, taskDirectory)
		if e != nil || !known {
			return nil
		}
		return value
	}}
}
