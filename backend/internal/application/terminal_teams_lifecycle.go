package application

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type terminalTeamsRuntime struct {
	mu      sync.Mutex
	closed  bool
	retired map[string]bool
	jobs    map[*terminalTeamsJob]struct{}
}
type terminalTeamsJob struct {
	cancel context.CancelFunc
	done   chan struct{}
	mount  bool
	id     string
}
type terminalTeamsPanelState struct {
	native                           *clibootstrap.NativeTerminalTeams
	snapshot                         *clibootstrap.NativeTerminalTeamsSnapshot
	memberIndices                    map[string]int
	mount                            *terminalTeamsJob
	members                          []protocol.JSONObject
	tasks                            []protocol.JSONObject
	teamName, level, memberName, err string
	detailIdentity                   string
	loading, supportsHideShow        bool
	detailRevision                   uint64
	selectionRevision                uint64
	selectionMaximum                 int
}

func newTerminalTeamsRuntime() *terminalTeamsRuntime {
	return &terminalTeamsRuntime{retired: map[string]bool{}, jobs: map[*terminalTeamsJob]struct{}{}}
}

// RetireTerminalTeams is a synchronous committed UI unmount. It also fences a
// mount whose queued command has not entered OpenTerminalTeams yet.
func (s *Session) RetireTerminalTeams(id string) {
	r := s.terminalTeams
	if r == nil {
		return
	}
	r.mu.Lock()
	r.retired[id] = true
	for job := range r.jobs {
		if job.mount && job.id == id {
			job.cancel()
		}
	}
	r.mu.Unlock()
}

func (s *Session) terminalTeamsMountLive(id string) bool {
	r := s.terminalTeams
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed && !r.retired[id]
}
func (s *Session) closeTerminalTeamsPanel(state *terminalLocalPanelState) {
	if state == nil {
		return
	}
	state.mu.Lock()
	var job *terminalTeamsJob
	if state.teamsPanel != nil {
		job = state.teamsPanel.mount
		state.teamsPanel.detailRevision++
	}
	id := state.props.ID
	state.mu.Unlock()
	if job != nil {
		s.RetireTerminalTeams(id)
		<-job.done
	}
}
func (s *Session) closeTerminalTeams() {
	r := s.terminalTeams
	if r == nil {
		return
	}
	r.mu.Lock()
	r.closed = true
	jobs := []*terminalTeamsJob{}
	for job := range r.jobs {
		job.cancel()
		jobs = append(jobs, job)
	}
	r.mu.Unlock()
	for _, job := range jobs {
		<-job.done
	}
}
func (s *Session) registerTerminalTeamsJob(ctx context.Context, id string, mount bool) (context.Context, *terminalTeamsJob, error) {
	r := s.terminalTeams
	if r == nil {
		return nil, nil, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.retired[id] {
		return nil, nil, context.Canceled
	}
	worker, cancel := context.WithCancel(ctx)
	job := &terminalTeamsJob{cancel: cancel, done: make(chan struct{}), id: id, mount: mount}
	r.jobs[job] = struct{}{}
	return worker, job, nil
}
func (s *Session) finishTerminalTeamsJob(job *terminalTeamsJob) {
	job.cancel()
	r := s.terminalTeams
	r.mu.Lock()
	delete(r.jobs, job)
	close(job.done)
	r.mu.Unlock()
}
