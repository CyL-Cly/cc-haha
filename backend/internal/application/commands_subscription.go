package application

import (
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// SubscribeCommands atomically installs the subscriber and takes the initial
// snapshot. Idle TUI menus therefore cannot miss a file change between reading
// their commands and registering a listener.
func (s *Session) SubscribeCommands(listener func([]tui.Completion)) ([]tui.Completion, func()) {
	s.mu.Lock()
	initial := tui.CloneCompletions(s.commandSnapshot)
	if listener == nil || s.closed {
		s.mu.Unlock()
		return initial, func() {}
	}
	s.commandListenerID++
	id := s.commandListenerID
	if s.commandListeners == nil {
		s.commandListeners = map[uint64]func([]tui.Completion){}
	}
	s.commandListeners[id] = listener
	s.mu.Unlock()
	var once sync.Once
	return initial, func() { once.Do(func() { s.mu.Lock(); delete(s.commandListeners, id); s.mu.Unlock() }) }
}

func (s *Session) commandsChanged(commands []sdkhost.ControlCommand) error {
	return s.setCommands(commands, true)
}

func (s *Session) setCommands(commands []sdkhost.ControlCommand, notify bool) error {
	completions := make([]tui.Completion, 0, len(commands))
	for _, command := range commands {
		completions = append(completions, tui.Completion{Name: command.Name, Description: command.Description, Kind: command.Kind, ArgumentHint: command.ArgumentHint, RequiresArguments: command.RequiresArguments, ArgumentNames: append([]string(nil), command.ArgumentNames...), Hidden: command.Hidden})
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.commandSnapshot = completions
	if !notify {
		s.mu.Unlock()
		return nil
	}
	listeners := make([]func([]tui.Completion), 0, len(s.commandListeners))
	for _, listener := range s.commandListeners {
		listeners = append(listeners, listener)
	}
	forward := s.onCommandsChanged
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(tui.CloneCompletions(completions))
	}
	if forward != nil {
		return forward(append([]sdkhost.ControlCommand{}, commands...))
	}
	return nil
}
