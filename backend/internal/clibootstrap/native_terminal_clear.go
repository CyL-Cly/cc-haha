package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"io"
	"os"
)

func (m *MainPrint) clearTerminalCachesOnOwner(scope *cooperative.Scope) error {
	m.systemContext = nil
	if m.userContext != nil {
		m.userContext.ClearTerminalSession()
	}
	if m.prompt != nil {
		m.prompt.Clear()
	}
	if m.memoryPaths != nil {
		m.memoryPaths.Clear()
	}
	if m.webFetch != nil {
		m.webFetch.ClearCache()
	}
	if m.nativeIngress != nil {
		m.nativeIngress.ClearAllSessions()
	}
	if m.toolSearch != nil {
		m.toolSearch.ClearDescriptionCache()
	}
	if m.nativeLSP != nil {
		m.nativeLSP.ResetDiagnostics()
	}
	if m.Git != nil {
		if err := m.Git.ClearDirs(scope); err != nil {
			return err
		}
		if err := m.Git.ClearRoots(scope); err != nil {
			return err
		}
	}
	if m.agents != nil {
		if err := m.agents.Clear(scope); err != nil {
			return err
		}
	}
	if m.skillCommands != nil {
		m.skillCommands.ClearDynamic()
		m.skillCommands.Clear()
	}
	m.dynamicSkillDirTriggers = nil
	m.userImages.Clear()
	if m.hookEnvironment != nil {
		m.hookEnvironment.Invalidate()
	}
	return m.resetNativeSkillListing(scope)
}

func (m *MainPrint) clearTerminalTaskOutputs(scope *cooperative.Scope, tasks sessioncontroller.TerminalClearTasks) error {
	for _, task := range tasks.Removed {
		path := task.String("outputFile")
		if path == "" {
			continue
		}
		if err := m.options.Native.check("clear foreground task output", path); err != nil {
			return err
		}
		if _, err := nativeSkillWork(m, scope, func() (struct{}, error) { _ = os.Remove(path); return struct{}{}, nil }); err != nil {
			return err
		}
	}
	for _, task := range tasks.RunningAgents {
		path, _, _, err := m.Controller.AgentTranscriptSourceOnOwner(scope, task.String("agentId"))
		if err != nil {
			return err
		}
		output := task.String("outputFile")
		if output == "" {
			continue
		}
		if err := m.options.Native.check("clear preserved task output", output); err != nil {
			return err
		}
		if _, err := nativeSkillWork(m, scope, func() (struct{}, error) {
			_ = os.Remove(output)
			_ = os.Symlink(path, output)
			return struct{}{}, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// TerminalClear runs the original idle REPL lifecycle on the existing graph.
// The old transcript is never rewritten; the next actual Record uses the new
// canonical bootstrap SID after the original file-pointer reset.
func (m *MainPrint) TerminalClear(ctx context.Context, args ...string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if m.closed || m.shuttingDown || !m.options.Native.NativeProcess || !m.options.Interactive || m.agentChild {
			return cooperative.ErrRestricted
		}
		argument := ""
		if len(args) > 0 {
			argument = args[0]
		}
		localInput, err := m.Host.PrepareTerminalClearLocalInputOnOwner(scope, argument)
		if err != nil {
			return err
		}
		// Preserve the original controller admission before command hook side effects.
		if _, err := m.Controller.TerminalClearTasksOnOwner(scope); err != nil {
			return err
		}
		budget := queryhooks.SessionEndTimeout(m.options.Native.Environment["CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS"])
		endCtx, cancel := context.WithTimeout(context.Background(), budget)
		err = m.hook.ExecuteSessionEnd(endCtx, "clear", queryhooks.Context{Scope: scope}, budget, func(line json.RawMessage) error {
			var text string
			if err := json.Unmarshal(line, &text); err != nil {
				return err
			}
			if m.options.Error == nil {
				return nil
			}
			_, err := io.WriteString(m.options.Error, text)
			return err
		})
		cancel()
		if err != nil {
			return err
		}
		// SessionEnd can await background agents. Read their current state after
		// the hook so completed outputs keep their original transcript target.
		tasks, err := m.Controller.TerminalClearTasksOnOwner(scope)
		if err != nil {
			return err
		}
		conversationID := uuidMain()
		if err = m.Host.ClearTerminalHistoryOnOwner(scope); err != nil {
			return err
		}
		if err = m.clearTerminalCachesOnOwner(scope); err != nil {
			return err
		}
		if err = m.setWorkingDirectory(scope, m.state.Snapshot().OriginalCWD); err != nil {
			return err
		}
		if err = m.Controller.ClearTerminalStateOnOwner(scope, tasks); err != nil {
			return err
		}
		sid, err := m.Controller.RegenerateTerminalSessionOnOwner(scope)
		if err != nil {
			return err
		}
		if err = m.clearNativeMCPForTerminalOnOwner(ctx, scope); err != nil {
			return err
		}
		if err = m.clearTerminalTaskOutputs(scope, tasks); err != nil {
			return err
		}
		if m.worktreeState != nil && m.worktreeState.current != nil {
			if err = m.saveTranscriptWorktree(scope, m.worktreeState.current); err != nil {
				return err
			}
		}
		start, err := m.hook.BeginSessionStart(scope, m.Owner, sid, "clear")
		if err != nil {
			return err
		}
		if _, err = start.Await(scope); err != nil {
			return err
		}
		hooks, err := m.hook.TakeSessionStartResult(scope, sid)
		if err != nil {
			return err
		}
		messages := []queryloop.Message{}
		for _, record := range hooks.Messages {
			messages = append(messages, queryloop.Message{Record: record})
		}
		if err = m.Host.InstallTerminalClearHookMessages(scope, messages); err != nil {
			return err
		}
		if err = m.Host.AppendTerminalClearLocalReplayOnOwner(scope, localInput); err != nil {
			return err
		}
		if err = m.refreshManagedSkillCommandsOnOwner(ctx, scope); err != nil {
			return err
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "sessionId", Value: quoteMain(sid)}, protocol.JSONField{Name: "conversationId", Value: quoteMain(conversationID)}, protocol.JSONField{Name: "history", Value: rawMain(m.Controller.History())})
		return nil
	})
	return result, err
}
