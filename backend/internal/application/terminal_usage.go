package application

import (
	"context"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalUsageError(err error) string {
	text := "Failed to load usage data"
	var response *clibootstrap.TerminalUsageHTTPError
	if errors.As(err, &response) && openaiauth.AuthTruthy(response.Body) {
		if body, e := protocol.ParseAndStringifyJavaScriptJSON(response.Body); e == nil {
			text += ": " + string(body)
		}
	}
	return text
}
func (s *Session) refreshTerminalUsagePanel(_ context.Context, state *terminalLocalPanelState) error {
	if state.usagePanel == nil {
		state.usagePanel = &terminalUsagePanelState{loading: true}
	}
	value := state.usagePanel
	state.props.Title = "Usage"
	state.props.Description = ""
	state.props.Rows = nil
	state.props.Fields = nil
	state.props.Presentation = raw(map[string]any{"value": value.value, "error": value.err, "loading": value.loading})
	state.props.Actions = nil
	if value.err != "" && !value.loading {
		state.props.Actions = []tui.LocalPanelAction{{Key: "r", Name: "usage-retry", Label: "retry"}}
	}
	return nil
}
func (s *Session) startTerminalUsagePanel(ctx context.Context, state *terminalLocalPanelState, emit tui.Emit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime := s.terminalUsage
	if runtime == nil {
		return errors.New("Usage lifecycle unavailable")
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return context.Canceled
	}
	state.mu.Lock()
	if state.usagePanel == nil {
		state.usagePanel = &terminalUsagePanelState{}
	}
	if state.usagePanel.loading && state.usagePanel.job != nil {
		state.mu.Unlock()
		runtime.mu.Unlock()
		return nil
	}
	// React's mounted Usage component owns this request independently of the
	// local command's completed ASK turn. Its actual unmount/global close paths
	// cancel and join the registered job; the API keeps its original 5s budget.
	workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	job := &terminalUsageJob{cancel: cancel, done: make(chan struct{})}
	value := state.usagePanel
	value.generation++
	generation := value.generation
	value.loading = true
	value.err = ""
	value.job = job
	_ = s.refreshTerminalUsagePanel(ctx, state)
	initial := state.props
	state.mu.Unlock()
	runtime.jobs[job] = struct{}{}
	runtime.mu.Unlock()
	if emit != nil {
		if err := emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &initial, Background: true}); err != nil {
			cancel()
		}
	}
	go func() {
		defer func() { cancel(); runtime.mu.Lock(); delete(runtime.jobs, job); close(job.done); runtime.mu.Unlock() }()
		result, err := s.process.TerminalUsage(workerCtx)
		state.mu.Lock()
		if value.generation != generation || workerCtx.Err() != nil || state.props.Kind != "usage" {
			state.mu.Unlock()
			return
		}
		value.loading = false
		value.job = nil
		if err != nil {
			value.err = terminalUsageError(err)
		} else {
			value.value = result.Clone()
		}
		_ = s.refreshTerminalUsagePanel(ctx, state)
		panel := state.props
		s.mu.Lock()
		active := !s.closed && s.terminalPanel == state && state.props.ID == panel.ID
		s.mu.Unlock()
		state.mu.Unlock()
		if active && emit != nil {
			_ = emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel, Background: true})
		}
	}()
	return nil
}
func (s *Session) closeTerminalUsagePanel(state *terminalLocalPanelState) {
	if state == nil {
		return
	}
	state.mu.Lock()
	var job *terminalUsageJob
	if value := state.usagePanel; value != nil {
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
func (s *Session) closeTerminalUsage() {
	runtime := s.terminalUsage
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.closed = true
	jobs := []*terminalUsageJob{}
	for job := range runtime.jobs {
		jobs = append(jobs, job)
		job.cancel()
	}
	runtime.mu.Unlock()
	for _, job := range jobs {
		<-job.done
	}
}
func (s *Session) applyTerminalUsagePanel(ctx context.Context, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	s.mu.Lock()
	state := s.terminalPanel
	s.mu.Unlock()
	if state == nil || state.props.ID != result.ID || state.props.Kind != "usage" {
		return false, nil
	}
	if result.Action == "close" {
		s.closeTerminalUsagePanel(state)
		s.mu.Lock()
		if s.terminalPanel == state {
			s.terminalPanel = nil
		}
		s.mu.Unlock()
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.LocalPanelClosed})
		}
		return true, nil
	}
	if result.Action == "usage-retry" || result.Action == "retry" {
		state.mu.Lock()
		canRetry := state.usagePanel != nil && state.usagePanel.err != "" && !state.usagePanel.loading
		state.mu.Unlock()
		if canRetry {
			return true, s.startTerminalUsagePanel(ctx, state, emit)
		}
		return true, nil
	}
	if strings.HasPrefix(result.Action, "settings-") {
		return false, nil
	}
	return true, errors.New("Unknown usage panel action")
}
