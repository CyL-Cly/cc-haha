package tui

import "time"

// The mounted REPL registers its root handlers before Help. Its chord
// interceptor sees every registered context, while individual single-key
// handlers retain their own context and propagation order.
func (s *State) handleTerminalRootHelpKey(key Key, now time.Time) (bool, []action) {
	p := s.LocalPanel
	if p == nil || !p.props.HelpDialog {
		return false, nil
	}
	contexts := []string{"Global", "Chat", "Help"}
	if p.headerFocused {
		contexts = append(contexts, "Tabs")
	} else {
		contexts = append(contexts, "Select")
	}
	if s.fullscreen {
		contexts = append(contexts, "Scroll")
	}
	if s.Transcript {
		contexts = append(contexts, "Transcript")
	}
	pending := len(s.pendingChord) != 0
	intercepted := ResolveChord(keyStroke(key), contexts, s.bindings, s.pendingChord)
	switch intercepted.Type {
	case "chord_started":
		s.pendingChord, s.chordUntil = intercepted.Pending, now.Add(time.Second)
		return true, nil
	case "chord_cancelled", "unbound":
		s.pendingChord = nil
		return true, nil
	case "match":
		if pending {
			if handled, actions := s.terminalRootHelpAction(intercepted.Action, now); handled {
				s.pendingChord = nil
				return true, actions
			}
		}
	}
	global := ResolveKey(keyStroke(key), []string{"Global"}, s.bindings)
	if global.Type == "unbound" {
		s.pendingChord = nil
		return true, nil
	}
	if global.Type == "match" {
		if handled, actions := s.terminalRootHelpAction(global.Action, now); handled {
			s.pendingChord = nil
			return true, actions
		}
	}
	if s.Transcript {
		transcript := ResolveKey(keyStroke(key), []string{"Global", "Transcript"}, s.bindings)
		if transcript.Type == "match" {
			if handled, actions := s.terminalRootHelpAction(transcript.Action, now); handled {
				s.pendingChord = nil
				return true, actions
			}
		}
	}
	cancel := ResolveKey(keyStroke(key), []string{"Global", "Chat"}, s.bindings)
	if cancel.Type == "match" && cancel.Action == "chat:killAgents" {
		s.pendingChord = nil
		return s.handleLocalDisplayAction(cancel.Action, now)
	}
	return false, nil
}

func (s *State) terminalRootHelpAction(name string, now time.Time) (bool, []action) {
	switch name {
	case "app:toggleTodos", "app:toggleTeammatePreview", "chat:killAgents":
		return s.handleLocalDisplayAction(name, now)
	case "app:toggleTranscript":
		s.Transcript = !s.Transcript
		s.transcriptShowAll, s.Scroll = false, 0
		s.remountTerminalHelp()
		return true, nil
	case "transcript:exit":
		if !s.Transcript {
			return false, nil
		}
		s.Transcript, s.transcriptShowAll, s.Scroll = false, false, 0
		s.remountTerminalHelp()
		return true, nil
	case "transcript:toggleShowAll":
		if s.Transcript && !s.fullscreen {
			s.transcriptShowAll = !s.transcriptShowAll
			return true, nil
		}
		// Its Source hook is inactive in the virtual transcript.
		return false, nil
	case "app:redraw":
		return true, []action{{kind: actionRedraw}}
	case "app:toggleTerminal":
		// The handler remains registered, but the frozen TERMINAL_PANEL build
		// feature disables its native action.
		return true, nil
	}
	return false, nil
}

func (s *State) remountTerminalHelp() {
	if p := s.LocalPanel; p != nil && p.props.HelpDialog {
		p.helpTab, p.selected, p.scroll = "general", 0, 0
		p.headerFocused = true
		p.helpExitTimes, p.helpExitPending, p.helpPaint = nil, "", nil
	}
	s.pendingChord = nil
}
