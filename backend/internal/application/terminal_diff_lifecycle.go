package application

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/terminaldiff"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type terminalDiffJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type terminalDiffRuntime struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	jobs   map[string]*terminalDiffJob
}

func (s *Session) startTerminalDiff(ctx context.Context, state *terminalLocalPanelState, initial terminaldiff.Dialog, emit tui.Emit) error {
	r := s.terminalDiff
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return context.Canceled
	}
	workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	job := &terminalDiffJob{cancel, make(chan struct{})}
	r.jobs[state.props.ID] = job
	r.mu.Unlock()
	go func() {
		defer func() { cancel(); r.mu.Lock(); delete(r.jobs, state.props.ID); close(job.done); r.mu.Unlock() }()
		value, err := s.process.TerminalDiff(workerCtx)
		if workerCtx.Err() != nil {
			return
		}
		// useTurnDiffs captured the messages prop on mount, independently of the
		// asynchronous current Git promise. Completion must not replace it.
		if len(initial.Sources) > 0 {
			initial.Sources[0].Data = terminaldiff.Data{Files: []terminaldiff.File{}}
			if err == nil && len(value.Sources) > 0 {
				initial.Sources[0] = value.Sources[0]
			}
		}
		state.mu.Lock()
		state.props.Presentation = raw(initial)
		panel := state.props
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
func (s *Session) closeTerminalDiffPanel(state *terminalLocalPanelState) {
	if state == nil || state.props.Kind != "diff" || s.terminalDiff == nil {
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
func (s *Session) closeTerminalDiff() {
	r := s.terminalDiff
	if r == nil {
		return
	}
	r.mu.Lock()
	r.closed = true
	if r.done != nil {
		close(r.done)
		r.done = nil
	}
	jobs := []*terminalDiffJob{}
	for _, job := range r.jobs {
		job.cancel()
		jobs = append(jobs, job)
	}
	r.mu.Unlock()
	for _, job := range jobs {
		<-job.done
	}
}
