package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/terminaldiff"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalBranchDiffCommands() []tui.Completion {
	return []tui.Completion{{Name: "diff", Description: "View uncommitted changes and per-turn diffs", Kind: "local-jsx"}, {Name: "branch", Description: "Create a branch of the current conversation at this point", ArgumentHint: "[name]", Kind: "local-jsx"}, {Name: "fork", Description: "Create a branch of the current conversation at this point", ArgumentHint: "[name]", Kind: "local-jsx"}}
}
func (s *Session) invokeTerminalBranchDiffCommand(ctx context.Context, name, args string, emit tui.Emit) (bool, error) {
	if name != "diff" && name != "branch" && name != "fork" || !s.terminalInteractive {
		return false, nil
	}
	if name == "branch" || name == "fork" {
		result, err := s.process.TerminalBranch(ctx, args)
		if err != nil {
			if emit == nil {
				return true, nil
			}
			return true, s.completeTerminalLocalJSX(ctx, "branch", args, raw("Failed to branch conversation: "+err.Error()), "", emit)
		}
		s.mu.Lock()
		s.terminalPanel = nil
		s.mu.Unlock()
		if emit == nil {
			return true, nil
		}
		if err = emit(tui.Event{Kind: tui.ConversationResumed, Result: raw(result)}); err != nil {
			return true, err
		}
		if err = s.completeTerminalLocalJSX(ctx, "branch", args, raw(result.String("text")), "system", emit); err != nil {
			return true, err
		}
		return true, s.emitTerminalPresentation(ctx, emit)
	}
	snapshot, err := s.process.TerminalConversation(ctx)
	if err != nil {
		return true, err
	}
	turns, err := terminaldiff.Turns(snapshot.Messages)
	if err != nil {
		return true, err
	}
	value := terminaldiff.Dialog{Sources: append([]terminaldiff.Source{{Data: terminaldiff.Data{Loading: true, Files: []terminaldiff.File{}}}}, turns...)}
	state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: "diff", Title: "Uncommitted changes", Presentation: raw(value)}, rows: map[string]protocol.JSONObject{}, form: args}
	s.mu.Lock()
	s.terminalPanel = state
	s.mu.Unlock()
	// Emit an immutable mount projection before the asynchronous producer may
	// update state.props. Event consumers may retain this exact value.
	initial := state.props
	if emit != nil {
		if err = emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &initial}); err != nil {
			return true, err
		}
	}
	return true, s.startTerminalDiff(ctx, state, value, emit)
}
func (s *Session) applyTerminalBranchDiffPanel(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	if state == nil || state.props.Kind != "diff" {
		return false, nil
	}
	if result.Action != "diff-close" {
		return true, nil
	}
	s.mu.Lock()
	if s.terminalPanel == state {
		s.terminalPanel = nil
	}
	s.mu.Unlock()
	// The completion callback takes state.mu to publish. Unmount before
	// joining, and release the panel lock while its cancelled worker settles.
	state.mu.Unlock()
	s.closeTerminalDiffPanel(state)
	state.mu.Lock()
	if emit == nil {
		return true, nil
	}
	if err := emit(tui.Event{Kind: tui.LocalPanelClosed, Background: true}); err != nil {
		return true, err
	}
	return true, s.completeTerminalLocalJSX(ctx, "diff", state.form, raw("Diff dialog dismissed"), "", emit)
}
