package tui

import (
	"strings"
	"time"
)

// The Source query reservation exists before an asynchronous local command's
// first UI update. This receipt covers that admission-to-return interval only;
// mounted panels retain their own input and background-work gates.
func (s *State) beginTerminalLocalCommand(name string) uint64 {
	if (!strings.HasPrefix(name, "command:panel:") && !strings.HasPrefix(name, "command:")) || s.ActiveTurn != "" || s.TeammateTurnID != "" || s.RemoteState != nil || len(s.pendingLocalCommands) > 0 {
		return 0
	}
	s.localCommandSequence++
	if s.pendingLocalCommands == nil {
		s.pendingLocalCommands = map[uint64]struct{}{}
	}
	s.pendingLocalCommands[s.localCommandSequence] = struct{}{}
	return s.localCommandSequence
}

func (s *State) finishTerminalLocalCommand(token uint64, err error) []action {
	defer s.syncTerminalSpinnerClock(time.Now())
	if _, ok := s.pendingLocalCommands[token]; !ok || token == 0 {
		return nil
	}
	delete(s.pendingLocalCommands, token)
	if err != nil {
		s.Status = err.Error()
	}
	return s.nextTerminalQueuedInput()
}

func (s *State) nextTerminalQueuedInput() []action {
	if s.Busy() || s.Closing || s.cancelling || s.LocalPanel != nil || s.ModelPicker != nil || s.IDEPicker != nil || s.ThinkingPicker != nil || s.PluginHint != nil || s.MessageSelector != nil || len(s.Permissions) > 0 || len(s.Queue) == 0 {
		return nil
	}
	next := s.Queue[0]
	s.Queue = s.Queue[1:]
	return s.start(next)
}
