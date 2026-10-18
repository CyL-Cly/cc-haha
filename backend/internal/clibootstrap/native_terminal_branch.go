package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
)

// TerminalBranch invokes createSessionBranch's physical reducer before the
// existing REPL resume callback. It does not flush or rewrite the old parent.
func (m *MainPrint) TerminalBranch(ctx context.Context, argument string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed || m.shuttingDown || !m.options.Interactive || m.agentChild || m.teamWorker != nil || m.Host == nil {
			return cooperative.ErrRestricted
		}
		if err := m.Host.CheckTerminalResumeOnOwner(scope); err != nil {
			return err
		}
		oldSID := m.Controller.SessionID()
		path, err := m.Controller.Writer().ComputedTranscriptPath(scope)
		if err != nil {
			return err
		}
		title := protocol.TrimJavaScriptString(argument)
		var customTitle json.RawMessage
		if title != "" {
			customTitle = quoteMain(title)
		}
		cwd := m.state.Snapshot().OriginalCWD
		branch, err := nativeSkillWork(m, scope, func() (sessionhistory.BranchResult, error) {
			return m.insightsHistory.Branch(sessionhistory.BranchOptions{SourceSessionID: oldSID, SourceTranscriptPath: path, SourceWorkDir: quoteMain(cwd), Title: customTitle, UUID: func() (string, error) { return uuidMain(), nil }, Now: m.options.Native.Now})
		})
		if err != nil {
			return err
		}
		copied, err := branch.Await(scope)
		if err != nil {
			return err
		}
		now := m.options.Native.Now()
		firstPrompt := sessionhistory.DeriveBranchFirstPrompt(nil)
		for _, row := range copied.SerializedMessages {
			var record protocol.JSONObject
			if err := json.Unmarshal(row, &record); err != nil {
				return err
			}
			if record.String("type") == "user" {
				firstPrompt = sessionhistory.DeriveBranchFirstPrompt(&record)
				break
			}
		}
		log := protocol.NewJSONObject(
			protocol.JSONField{Name: "date", Value: quoteMain(now.UTC().Format("2006-01-02"))},
			protocol.JSONField{Name: "messages", Value: rawMain(copied.SerializedMessages)},
			protocol.JSONField{Name: "fullPath", Value: quoteMain(copied.ForkPath)},
			protocol.JSONField{Name: "value", Value: rawMain(now.UnixMilli())},
			protocol.JSONField{Name: "created", Value: quoteMain(now.UTC().Format("2006-01-02T15:04:05.000Z"))},
			protocol.JSONField{Name: "modified", Value: quoteMain(now.UTC().Format("2006-01-02T15:04:05.000Z"))},
			protocol.JSONField{Name: "firstPrompt", Value: firstPrompt},
			protocol.JSONField{Name: "messageCount", Value: rawMain(len(copied.SerializedMessages))},
			protocol.JSONField{Name: "isSidechain", Value: rawMain(false)},
			protocol.JSONField{Name: "sessionId", Value: quoteMain(copied.SessionID)},
			protocol.JSONField{Name: "customTitle", Value: copied.Title},
			protocol.JSONField{Name: "contentReplacements", Value: rawMain(copied.ContentReplacementRecords)},
		)
		if err = m.resumeTerminalLogOnOwner(ctx, scope, log, true); err != nil {
			return err
		}
		history, err := querypreflight.InspectionRecords(m.Host.History())
		if err != nil {
			return err
		}
		text := "Branched conversation"
		if title != "" {
			text += " \"" + title + "\""
		}
		text += ". You are now in the branch.\nTo resume the original: claude -r " + oldSID
		result = protocol.NewJSONObject(protocol.JSONField{Name: "sessionId", Value: quoteMain(copied.SessionID)}, protocol.JSONField{Name: "originalSessionId", Value: quoteMain(oldSID)}, protocol.JSONField{Name: "history", Value: rawMain(history)}, protocol.JSONField{Name: "text", Value: quoteMain(text)})
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}

// resumeTerminalLogOnOwner is the actual REPL callback, distinct from the
// headless startup producer. The selected LogOption is original persisted data.
func (m *MainPrint) resumeTerminalLogOnOwner(ctx context.Context, scope *cooperative.Scope, log protocol.JSONObject, fork bool) error {
	value, err := m.Host.PrepareTerminalResumeOnOwner(scope, log)
	if err != nil {
		return err
	}
	budget := queryhooks.SessionEndTimeout(m.options.Native.Environment["CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS"])
	endCtx, cancel := context.WithTimeout(context.Background(), budget)
	err = m.hook.ExecuteSessionEnd(endCtx, "resume", queryhooks.Context{Scope: scope}, budget, func(line json.RawMessage) error {
		var text string
		if err := json.Unmarshal(line, &text); err != nil {
			return err
		}
		_, err := io.WriteString(m.options.Error, text)
		return err
	})
	cancel()
	if err != nil {
		return err
	}
	start, err := m.hook.BeginSessionStart(scope, m.Owner, log.String("sessionId"), "resume")
	if err != nil {
		return err
	}
	if _, err = start.Await(scope); err != nil {
		return err
	}
	hooks, err := m.hook.TakeSessionStartResult(scope, log.String("sessionId"))
	if err != nil {
		return err
	}
	if fork {
		if err = m.copyTerminalResumePlan(scope, log, true); err != nil {
			return err
		}
	} else if err = m.Controller.BeginTerminalResumePlanOnOwner(scope, value); err != nil {
		return err
	}
	if err = m.Controller.AppendTerminalResumeHooksOnOwner(scope, value, hooks.Messages); err != nil {
		return err
	}
	if err = m.Controller.BeginTerminalResumeFileHistoryOnOwner(scope, value); err != nil {
		return err
	}
	if err = m.restoreTerminalResumeAgent(scope, log); err != nil {
		return err
	}
	if err = m.Controller.RestoreTerminalResumeReadsOnOwner(scope, value, m.state.Snapshot().OriginalCWD); err != nil {
		return err
	}
	recovered, err := m.Controller.TerminalResumeRecoveredReadsOnOwner(scope, value)
	if err != nil {
		return err
	}
	for _, row := range recovered.Entries() {
		path := row.String("path")
		next, _ := recovered.Get(path)
		previous, exists, err := m.nativeFileCache.Get(scope, path)
		if err != nil {
			return err
		}
		if !exists || next.Timestamp > previous.Timestamp {
			if err = m.nativeFileCache.Set(scope, path, builtinwrite.FileState{Content: next.Content, Timestamp: next.Timestamp}); err != nil {
				return err
			}
		}
	}
	key, err := m.originalProjectConfigKey(scope)
	if err != nil {
		return err
	}
	// Capture target before saving old costs, because both live in one original
	// project config record. Keep a detached copy through the subsequent write.
	target, err := m.Global.GetProject(key)
	if err != nil {
		return err
	}
	ledger, err := m.Controller.ModelAccountingFor(m.Owner)
	if err != nil {
		return err
	}
	resolver := modelaccounting.RestoredModelResolver{Environment: m.options.Native.Environment, Settings: m.Startup.GetInitialSettings, Global: m.Global.Get, SDKBetas: m.nativeSDKBetas}
	targetCosts, err := ledger.CaptureTerminalTargetCosts(scope, log.String("sessionId"), target, resolver)
	if err != nil {
		return err
	}
	fields, err := m.Host.TerminalCostFieldsOnOwner(scope)
	if err != nil {
		return err
	}
	if err = m.Global.SaveProject(key, func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		for _, field := range fields.OwnProperties() {
			if err := next.SetPropertyJSON(field.NameJSON, field.Value); err != nil {
				return nil, err
			}
		}
		next.Delete("lastFpsAverage")
		next.Delete("lastFpsLow1Pct")
		return &next, nil
	}); err != nil {
		return err
	}
	if err = m.Host.ResetTerminalCostsOnOwner(scope); err != nil {
		return err
	}
	if err = m.Controller.SwitchTerminalResumeSessionOnOwner(scope, value); err != nil {
		return err
	}
	// REPL awaits the asciicast module then its async rename. The external
	// distribution never installs a recorder; each no-op remains a Promise
	// reaction on the prelinked module, without inventing filesystem I/O.
	if err = scope.AwaitFulfilled(); err != nil {
		return err
	}
	if err = scope.AwaitFulfilled(); err != nil {
		return err
	}
	if err = m.Controller.ResetTerminalResumeMetadataOnOwner(scope, value); err != nil {
		return err
	}
	if !fork {
		if err = m.restoreTerminalResumeWorktree(scope, log.Get("worktreeSession")); err != nil {
			return err
		}
		if err = m.restoreNativeRemoteAgentTasks(scope); err != nil {
			return err
		}
	} else if m.worktreeState != nil && m.worktreeState.current != nil {
		if err = m.saveTranscriptWorktree(scope, m.worktreeState.current); err != nil {
			return err
		}
	}
	if _, err = ledger.RestoreTerminalTargetCosts(scope, targetCosts, m.options.Native.NowMillis); err != nil {
		return err
	}
	return m.Host.InstallTerminalResumeOnOwner(scope, value)
}

func (m *MainPrint) copyTerminalResumePlan(scope *cooperative.Scope, log protocol.JSONObject, fork bool) error {
	var rows []protocol.JSONObject
	if err := json.Unmarshal(log.Get("messages"), &rows); err != nil {
		return err
	}
	slug := ""
	for _, row := range rows {
		if row.String("slug") != "" {
			slug = row.String("slug")
			break
		}
	}
	if slug == "" {
		return nil
	}
	if !fork {
		return mainCap("ordinary interactive plan restore consumer")
	}
	dir, err := m.plans.Directory(scope)
	if err != nil {
		return err
	}
	newSlug, err := m.plans.SlugForSession(scope, log.String("sessionId"))
	if err != nil {
		return err
	}
	oldPath, newPath := filepath.Join(dir, slug+".md"), filepath.Join(dir, newSlug+".md")
	future, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		return struct{}{}, (mainResumeFiles{n: m.options.Native}).CopyFile(oldPath, newPath)
	})
	if err != nil {
		return err
	}
	// Original copyPlanForFork is fire-and-forget. It catches ENOENT and logs
	// other errors, retaining the same native lifetime until its real copy ends.
	_, err = future.Then(scope, func(_ *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil && !errors.Is(failure, os.ErrNotExist) {
			_, _ = fmt.Fprintln(m.options.Error, failure.Error())
		}
		return nil
	})
	return err
}
