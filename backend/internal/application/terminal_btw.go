package application

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalBtwCommands() []tui.Completion {
	return []tui.Completion{{Name: "btw", Description: "Ask a quick side question without interrupting the main conversation", ArgumentHint: "<question>", Kind: "local-jsx"}}
}

func (s *Session) completeTerminalBtwUsage(ctx context.Context, args string, emit tui.Emit) error {
	_, fullscreen, err := s.initialTUIHistory(ctx)
	if err != nil {
		return err
	}
	records, immediate, err := s.process.CompleteTerminalBtwUsage(ctx, args, fullscreen)
	if err != nil || emit == nil {
		return err
	}
	if immediate {
		if err = emit(tui.Event{Kind: tui.TerminalImmediateNotice, Text: "Usage: /btw <your question>", Background: true}); err != nil {
			return err
		}
	}
	if len(records) == 0 {
		return nil
	}
	return s.localInputMessagesTo(records, func(event tui.Event) error {
		event.Background = true
		event.TurnID = records[0].String("uuid")
		return emit(event)
	})
}

func (s *Session) invokeTerminalBtwCommand(ctx context.Context, name, args string, emit tui.Emit) (bool, error) {
	if name != "btw" || !s.terminalInteractive {
		return false, nil
	}
	question, immediate, err := s.process.PrepareTerminalBtwState(ctx, args)
	if err != nil {
		return true, err
	}
	if question == "" {
		return true, s.completeTerminalBtwUsage(ctx, args, emit)
	}
	state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: "btw", HideDetails: true, KeepPromptInput: immediate}, rows: map[string]protocol.JSONObject{}, btwPanel: &terminalBtwPanelState{question: question}}
	s.mu.Lock()
	previous := s.terminalPanel
	s.mu.Unlock()
	s.closeTerminalUsagePanel(previous)
	s.closeTerminalMCPAuthPanel(previous)
	s.closeTerminalBtwPanel(previous)
	s.refreshTerminalBtwPanel(state)
	if err = s.openTerminalConversationPanel(state, emit); err != nil {
		return true, err
	}
	return true, s.startTerminalBtw(ctx, state, emit)
}

func (s *Session) refreshTerminalBtwPanel(state *terminalLocalPanelState) {
	value := state.btwPanel
	state.props.Presentation = raw(map[string]any{"question": value.question, "response": value.response, "error": value.err, "ghostty": s.native.Environment["TERM"] == "xterm-ghostty"})
	state.props.Rows = nil
	state.props.Fields = nil
}

func (s *Session) startTerminalBtw(ctx context.Context, state *terminalLocalPanelState, emit tui.Emit) error {
	r := s.terminalBtw
	if r == nil {
		return errors.New("Side question lifecycle unavailable")
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return context.Canceled
	}
	workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	job := &terminalBtwJob{cancel: cancel, done: make(chan struct{})}
	state.mu.Lock()
	value := state.btwPanel
	value.generation++
	generation := value.generation
	value.job = job
	id := state.props.ID
	question := value.question
	state.mu.Unlock()
	r.jobs[job] = struct{}{}
	r.mu.Unlock()
	go func() {
		defer func() { cancel(); r.mu.Lock(); delete(r.jobs, job); close(job.done); r.mu.Unlock() }()
		result, err := s.process.TerminalBtwQuestion(workerCtx, id, question)
		state.mu.Lock()
		if workerCtx.Err() != nil || value.generation != generation {
			state.mu.Unlock()
			return
		}
		value.job = nil
		if err != nil {
			value.err = err.Error()
			if value.err == "" {
				value.err = "Failed to get response"
			}
		} else if result.String("response") == "" {
			value.err = "No response received"
		} else {
			value.response = result.String("response")
		}
		s.refreshTerminalBtwPanel(state)
		panel := terminalPanelProjection(state.props)
		s.mu.Lock()
		active := !s.closed && s.terminalPanel == state
		s.mu.Unlock()
		state.mu.Unlock()
		if active && emit != nil {
			_ = emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel, Background: true})
		}
	}()
	return nil
}

func (s *Session) closeTerminalBtwPanel(state *terminalLocalPanelState) {
	if state == nil {
		return
	}
	state.mu.Lock()
	var job *terminalBtwJob
	if value := state.btwPanel; value != nil {
		value.generation++
		job = value.job
		value.job = nil
	}
	state.mu.Unlock()
	if job != nil {
		job.cancel()
		<-job.done
	}
}
func (s *Session) closeTerminalBtw() {
	r := s.terminalBtw
	if r == nil {
		return
	}
	r.mu.Lock()
	r.closed = true
	jobs := []*terminalBtwJob{}
	for job := range r.jobs {
		job.cancel()
		jobs = append(jobs, job)
	}
	r.mu.Unlock()
	for _, job := range jobs {
		<-job.done
	}
}
func (s *Session) applyTerminalBtwPanel(_ context.Context, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	s.mu.Lock()
	state := s.terminalPanel
	s.mu.Unlock()
	if state == nil || state.props.ID != result.ID || state.props.Kind != "btw" {
		return false, nil
	}
	if result.Action != "btw-close" && result.Action != "close" {
		return true, nil
	}
	s.closeTerminalBtwPanel(state)
	// Source onDone(undefined,{display:'skip'}) contributes no replay records.
	return true, s.closeTerminalConversationPanel(state.props.ID, emit)
}
