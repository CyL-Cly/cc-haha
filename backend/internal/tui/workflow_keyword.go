package tui

import (
	"context"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/ultracode"
)

// WorkflowKeywordSource reads the actual interactive host setting. It grants
// no permission and performs no Agent execution from the composer.
type WorkflowKeywordSource interface {
	WorkflowKeywordEnabled(context.Context) (bool, error)
}
type workflowKeywordEnabledMsg struct {
	enabled bool
	err     error
}

func (s *State) SetWorkflowKeywordEnabled(enabled bool, now time.Time) {
	s.workflowKeywordEnabled = enabled
	s.syncWorkflowKeyword(now)
}
func (s *State) syncWorkflowKeyword(now time.Time) {
	present := s.workflowKeywordEnabled && ultracode.HasKeyword(s.Editor.Text())
	if !present {
		s.workflowKeywordSuppressed = false
	}
	active := present && !s.workflowKeywordSuppressed
	if active && !s.workflowKeywordActive {
		s.Notification = "Ultracode: this prompt runs as a workflow · opt+w to undo"
		s.notificationUntil = now.Add(5 * time.Second)
	}
	if !active && s.Notification == "Ultracode: this prompt runs as a workflow · opt+w to undo" {
		s.Notification = ""
		s.notificationUntil = time.Time{}
	}
	s.workflowKeywordActive = active
}
func (s *State) toggleWorkflowKeyword(now time.Time) {
	if !s.workflowKeywordEnabled || !ultracode.HasKeyword(s.Editor.Text()) {
		return
	}
	s.workflowKeywordSuppressed = !s.workflowKeywordSuppressed
	if s.workflowKeywordSuppressed {
		s.Notification = "Ultracode keyword ignored for this prompt · opt+w to undo"
		s.notificationUntil = now.Add(5 * time.Second)
	} else if s.Notification == "Ultracode keyword ignored for this prompt · opt+w to undo" {
		s.Notification = ""
		s.notificationUntil = time.Time{}
	}
	s.syncWorkflowKeyword(now)
}
