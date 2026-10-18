package application

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalResumeCommands() []tui.Completion {
	return []tui.Completion{{Name: "resume", Description: "Resume a previous conversation", ArgumentHint: "[conversation id or search term]", Kind: "local-jsx"}, {Name: "continue", Description: "Resume a previous conversation", ArgumentHint: "[conversation id or search term]", Kind: "local-jsx"}}
}
func (s *Session) invokeTerminalResumeCommand(ctx context.Context, name, args string, emit tui.Emit) (bool, error) {
	if (name != "resume" && name != "continue") || !s.terminalInteractive {
		return false, nil
	}
	state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: "resume", Title: "Resume Session", Presentation: raw(map[string]any{"loading": true})}, rows: map[string]protocol.JSONObject{}, resumePanel: &terminalResumePanelState{completion: make(chan struct{})}, form: args}
	s.mu.Lock()
	s.terminalPanel = state
	s.mu.Unlock()
	if emit != nil {
		panel := state.props
		if err := emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &panel}); err != nil {
			return true, err
		}
	}
	err := s.startTerminalResumeJob(ctx, state, emit, func(worker context.Context) error {
		if protocol.TrimJavaScriptString(args) != "" {
			selected, message, err := s.process.ResolveTerminalResumeArgument(worker, args)
			if err != nil {
				return s.finishTerminalResumePanel(worker, state, emit, "Failed to resume: "+err.Error(), nil)
			}
			if message != "" {
				return s.finishTerminalResumePanel(worker, state, emit, message, nil)
			}
			result, err := s.process.ResumeTerminalSelection(worker, selected)
			if err != nil {
				return s.finishTerminalResumePanel(worker, state, emit, "Failed to resume: "+err.Error(), nil)
			}
			return s.finishTerminalResumePanel(worker, state, emit, "", &result)
		}
		return s.loadTerminalResumePanel(worker, state, false, emit)
	})
	if err != nil {
		return true, err
	}
	// The original local-JSX processSlashCommand Promise resolves only on
	// onDone, including a no-argument picker after select/cancel. Merely loading
	// its rows does not complete the accepted command or admit the next queue item.
	select {
	case <-state.resumePanel.completion:
		return true, state.resumePanel.completionError
	case <-ctx.Done():
		return true, context.Cause(ctx)
	case <-s.terminalResumeRuntimeDone():
		return true, context.Canceled
	}
}
func (s *Session) startTerminalResumeJob(ctx context.Context, state *terminalLocalPanelState, emit tui.Emit, work func(context.Context) error) error {
	r := s.terminalDiff
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return context.Canceled
	}
	worker, cancel := context.WithCancel(context.WithoutCancel(ctx))
	job := &terminalDiffJob{cancel: cancel, done: make(chan struct{})}
	r.jobs[state.props.ID] = job
	r.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			r.mu.Lock()
			if r.jobs[state.props.ID] == job {
				delete(r.jobs, state.props.ID)
			}
			close(job.done)
			r.mu.Unlock()
		}()
		_ = work(worker)
	}()
	return nil
}
func (s *Session) loadTerminalResumePanel(ctx context.Context, state *terminalLocalPanelState, all bool, emit tui.Emit) error {
	catalog, err := s.process.ReadTerminalResumeCatalog(ctx, all)
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if err != nil {
		return s.finishTerminalResumePanel(ctx, state, emit, "Failed to load conversations", nil)
	}
	view := catalog.Presentation()
	var logs []protocol.JSONObject
	_ = json.Unmarshal(view.Get("logs"), &logs)
	if len(logs) == 0 {
		return s.finishTerminalResumePanel(ctx, state, emit, "No conversations found to resume", nil)
	}
	state.mu.Lock()
	state.resumePanel.catalog = catalog
	state.resumePanel.preview = nil
	state.resumePanel.all = all
	state.props.Presentation = raw(view)
	panel := state.props
	s.mu.Lock()
	active := !s.closed && s.terminalPanel == state
	s.mu.Unlock()
	state.mu.Unlock()
	if active && emit != nil {
		return emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel, Background: true})
	}
	return nil
}
func (s *Session) finishTerminalResumePanel(ctx context.Context, state *terminalLocalPanelState, emit tui.Emit, message string, result *protocol.JSONObject) (returned error) {
	defer func() {
		state.resumePanel.completionOnce.Do(func() { state.resumePanel.completionError = returned; close(state.resumePanel.completion) })
	}()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	s.mu.Lock()
	active := !s.closed && s.terminalPanel == state
	if active {
		s.terminalPanel = nil
	}
	s.mu.Unlock()
	if !active || emit == nil {
		return nil
	}
	if result != nil {
		if err := emit(tui.Event{Kind: tui.ConversationResumed, Result: raw(*result)}); err != nil {
			return err
		}
		return s.emitTerminalPresentation(ctx, emit)
	}
	if err := emit(tui.Event{Kind: tui.LocalPanelClosed, Background: true}); err != nil {
		return err
	}
	return s.completeTerminalLocalJSX(ctx, "resume", state.form, raw(message), state.resumePanel.completionDisplay, emit)
}
func (s *Session) applyTerminalResumePanel(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	if state == nil || state.props.Kind != "resume" {
		return false, nil
	}
	state.mu.Unlock()
	s.closeTerminalResumePanel(state)
	state.mu.Lock()
	if result.Action == "resume-cancel" {
		state.resumePanel.completionDisplay = "system"
		return true, s.finishTerminalResumePanel(ctx, state, emit, "Resume cancelled", nil)
	}
	if state.resumePanel == nil || state.resumePanel.catalog == nil {
		return true, nil
	}
	if result.Action == "resume-all" {
		all := !state.resumePanel.all
		state.props.Presentation = raw(map[string]any{"loading": true})
		panel := state.props
		if emit != nil {
			if err := emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel, Background: true}); err != nil {
				return true, err
			}
		}
		return true, s.startTerminalResumeJob(ctx, state, emit, func(worker context.Context) error { return s.loadTerminalResumePanel(worker, state, all, emit) })
	}
	if result.Action == "resume-preview" || result.Action == "resume-rename" {
		index, err := strconv.Atoi(result.Row)
		if err != nil {
			return true, err
		}
		catalog := state.resumePanel.catalog
		if result.Action == "resume-preview" {
			state.resumePanel.preview = nil
			state.resumePanel.previewIndex = index
			var view protocol.JSONObject
			_ = json.Unmarshal(state.props.Presentation, &view)
			view.Delete("preview")
			state.props.Presentation = raw(view)
			panel := state.props
			if emit != nil {
				if err := emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel, Background: true}); err != nil {
					return true, err
				}
			}
		}
		return true, s.startTerminalResumeJob(ctx, state, emit, func(worker context.Context) error {
			if result.Action == "resume-rename" {
				if err := s.process.RenameTerminalResume(worker, catalog, index, result.Fields["title"]); err != nil {
					return err
				}
				// The original onLogsChanged invokes loadLogs, whose immediate
				// setLoading(true) unmounts LogSelector before the new disk rows
				// arrive. Its local query/rename state therefore starts fresh.
				state.mu.Lock()
				state.props.Presentation = raw(map[string]any{"loading": true})
				panel := state.props
				state.mu.Unlock()
				if emit != nil {
					if err := emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel, Background: true}); err != nil {
						return err
					}
				}
				return s.loadTerminalResumePanel(worker, state, state.resumePanel.all, emit)
			}
			selected, err := s.process.SelectTerminalResume(worker, catalog, index)
			if err != nil {
				return err
			}
			state.mu.Lock()
			state.resumePanel.preview = selected
			var view protocol.JSONObject
			_ = json.Unmarshal(state.props.Presentation, &view)
			view.Set("preview", raw(selected.Presentation()))
			state.props.Presentation = raw(view)
			panel := state.props
			s.mu.Lock()
			active := !s.closed && s.terminalPanel == state
			s.mu.Unlock()
			state.mu.Unlock()
			if active && emit != nil {
				return emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel, Background: true})
			}
			return nil
		})
	}
	index, err := strconv.Atoi(result.Row)
	if err != nil {
		return true, err
	}
	catalog := state.resumePanel.catalog
	var captured *clibootstrap.TerminalResumeSelection
	if result.Action == "resume-preview-select" {
		if state.resumePanel.previewIndex != index {
			return true, cooperative.ErrRestricted
		}
		// SessionPreview's confirmation passes its already displayed fullLog.
		// A still-loading/failed lite preview instead follows handleSelect's
		// original loadFullLog path. Detached display JSON is never admitted.
		if state.resumePanel.preview.HasFullMessages() {
			captured = state.resumePanel.preview
		}
	} else if result.Action != "resume-select" {
		return true, cooperative.ErrRestricted
	}
	state.props.Presentation = raw(map[string]any{"resuming": true})
	panel := state.props
	if emit != nil {
		if err := emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel, Background: true}); err != nil {
			return true, err
		}
	}
	return true, s.startTerminalResumeJob(ctx, state, emit, func(worker context.Context) error {
		selected := captured
		var err error
		if selected == nil {
			selected, err = s.process.SelectTerminalResume(worker, catalog, index)
		}
		if err != nil {
			return s.finishTerminalResumePanel(worker, state, emit, "Failed to resume conversation", nil)
		}
		if command := s.process.TerminalResumeCrossProject(selected); command != "" {
			if err = s.terminalCopyToClipboard(worker, emit, command); err != nil {
				return err
			}
			return s.finishTerminalResumePanel(worker, state, emit, "\nThis conversation is from a different directory.\n\nTo resume, run:\n  "+command+"\n\n(Command copied to clipboard)\n", nil)
		}
		value, err := s.process.ResumeTerminalSelection(worker, selected)
		if err != nil {
			return s.finishTerminalResumePanel(worker, state, emit, "Failed to resume: "+err.Error(), nil)
		}
		return s.finishTerminalResumePanel(worker, state, emit, "", &value)
	})
}
func (s *Session) closeTerminalResumePanel(state *terminalLocalPanelState) {
	if state == nil || state.props.Kind != "resume" || s.terminalDiff == nil {
		return
	}
	r := s.terminalDiff
	r.mu.Lock()
	job := r.jobs[state.props.ID]
	if job != nil {
		job.cancel()
	}
	r.mu.Unlock()
	if job != nil {
		<-job.done
	}
}

func (s *Session) terminalResumeRuntimeDone() <-chan struct{} {
	r := s.terminalDiff
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done == nil {
		r.done = make(chan struct{})
		if r.closed {
			close(r.done)
		}
	}
	return r.done
}
