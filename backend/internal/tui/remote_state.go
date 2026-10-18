package tui

type TerminalRemoteState struct {
	Connection      string
	Loading         bool
	SessionURL      string
	BackgroundTasks int
}

func (s *State) terminalRemoteBusy() bool {
	return s.RemoteState != nil && s.RemoteState.Loading
}

// The Source remote loading state outlives the HTTP POST's fulfillment. Only
// actual incoming result/permission/connection callbacks update this state.
func (s *State) applyTerminalRemoteState(event Event) []action {
	if event.RemoteState == nil {
		return nil
	}
	value := *event.RemoteState
	s.RemoteState = &value
	if value.Loading || len(s.Permissions) != 0 || s.Closing {
		return nil
	}
	if s.ActiveTurn != "" {
		return s.FinishTurn(s.ActiveTurn, nil)
	}
	if len(s.Queue) == 0 || s.TeammateTurnID != "" {
		return nil
	}
	next := s.Queue[0]
	s.Queue = s.Queue[1:]
	return s.start(next)
}

func (s *State) terminalRemoteLines() []string {
	if s.RemoteState == nil {
		return nil
	}
	label := "Remote: " + s.RemoteState.Connection
	if s.RemoteState.SessionURL != "" {
		label += " · " + s.RemoteState.SessionURL
	}
	return []string{label}
}
