package tui

import "time"

// REPL owns these refs independently of the mounted Spinner. A permission or
// local JSX can unmount the component while its response count and clock live
// on. The stream observer is the only producer of mode and response length.
type terminalSpinnerClock struct {
	mode            string
	responseUnits   int
	verb            string
	verbKnown       bool
	loading         bool
	started         time.Time
	pausedAt        time.Time
	paused          time.Duration
	render          *terminalSpinnerRenderState
	parentRevision  uint64
	animationTime   time.Duration
	stream          *terminalSpinnerStreamState
	overrideMessage *string
	overrideColor   string
	suffix          string
}

func (s *State) ensureTerminalSpinnerClock() *terminalSpinnerClock {
	if s.spinnerClock == nil {
		s.spinnerClock = &terminalSpinnerClock{mode: "responding"}
	}
	return s.spinnerClock
}

func (s *State) syncTerminalSpinnerClock(now time.Time) {
	r := s.ensureTerminalSpinnerClock()
	loading := s.Busy()
	if loading && !r.loading {
		r.started, r.pausedAt, r.paused = now, time.Time{}, 0
	}
	if !loading && r.loading {
		// resetLoadingState clears the response ref, not streamMode or the
		// timing refs. The next query's reservation resets the latter.
		r.responseUnits = 0
	}
	r.loading = loading
	if !loading {
		return
	}
	paused := s.terminalSpinnerToolPermissionFocused()
	if paused && r.pausedAt.IsZero() {
		r.pausedAt = now
	} else if !paused && !r.pausedAt.IsZero() {
		r.paused += now.Sub(r.pausedAt)
		r.pausedAt = time.Time{}
	}
}

func (s *State) beginTerminalSpinnerQuery(now time.Time) {
	r := s.ensureTerminalSpinnerClock()
	r.started, r.pausedAt, r.paused = now, time.Time{}, 0
	r.responseUnits = 0
	r.loading = true
}

func (s *State) terminalSpinnerToolPermissionFocused() bool {
	// These are the actual REPL focus inputs represented by State. Teams is
	// PromptInput's own dialog and does not produce toolJSX. Ordinary slash
	// JSX blocks permission focus; AskUserQuestion is the separate prompt.
	return !s.Closing && s.MessageSelector == nil && !s.pluginHintPromptActive &&
		!s.taskListLocalJSXMounted() && len(s.Permissions) > 0 &&
		s.Permissions[0].ToolName != "AskUserQuestion"
}

func (r *terminalSpinnerClock) elapsed(now time.Time) time.Duration {
	if r == nil || r.started.IsZero() {
		return 0
	}
	if !r.pausedAt.IsZero() {
		now = r.pausedAt
	}
	return now.Sub(r.started) - r.paused
}

func (s *State) applyTerminalSpinnerProgress(progress *TerminalSpinnerProgress) {
	if progress == nil {
		return
	}
	r := s.ensureTerminalSpinnerClock()
	oldMode := r.mode
	if progress.Mode != "" {
		r.mode = progress.Mode
	}
	r.responseUnits += progress.AddedResponseUnits
	commit := s.applyTerminalSpinnerQuery(progress.QueryEvent, oldMode)
	if progress.Verb != nil {
		r.verb, r.verbKnown = *progress.Verb, true
		commit = true
	}
	if commit {
		s.commitTerminalSpinnerParent()
	}
}
