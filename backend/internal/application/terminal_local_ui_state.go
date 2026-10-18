package application

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// The terminal actor reports its actual local JSX input ownership. This
// detached flag is read by the existing idle queue job under the Session mutex.
func (s *Session) SetTerminalLocalUIActive(active bool) {
	s.mu.Lock()
	s.terminalLocalUIActive = active
	s.mu.Unlock()
}
func (s *Session) canStartTerminalIdleInputLocked() bool {
	return !s.closed && len(s.pending) == 0 && !s.terminalLocalUIActive && s.terminalPanel == nil
}

// Teams is local tool JSX in REPL, rather than a focused permission dialog.
// InboxPoller's incoming prompt can run while that dialog remains mounted.
// The ordinary command queue keeps its separate local JSX input guard.
func (s *Session) canStartTerminalTeamInboxLocked(inbox []protocol.JSONObject) bool {
	if s.terminalPanel != nil && s.terminalPanel.props.Kind == "teams" {
		for _, message := range inbox {
			if message.String("status") == "pending" {
				return !s.closed && len(s.pending) == 0
			}
		}
	}
	return s.canStartTerminalIdleInputLocked()
}
