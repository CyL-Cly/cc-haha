package tui

func (s *State) applyTerminalRewind(event Event) {
	if event.MessageSelector == nil {
		return
	}
	retained := make([]Entry, 0, len(s.Entries))
	for _, entry := range s.Entries {
		if entry.TurnID != event.TurnID || entry.Kind != "user" {
			retained = append(retained, entry)
		}
	}
	s.Entries = retained
	s.OpenMessageSelector(*event.MessageSelector)
}
