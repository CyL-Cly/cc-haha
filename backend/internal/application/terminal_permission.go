package application

import "sync"

type terminalPermissionState struct {
	mode      string
	sequence  uint64
	listeners map[uint64]func(string)
}

func (s *Session) permissionModeChanged(mode string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if s.terminalPermission == nil {
		s.terminalPermission = &terminalPermissionState{}
	}
	s.terminalPermission.mode = mode
	listeners := make([]func(string), 0, len(s.terminalPermission.listeners))
	for _, listener := range s.terminalPermission.listeners {
		listeners = append(listeners, listener)
	}
	forward := s.processPermissionModeChanged
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(mode)
	}
	if forward != nil {
		return forward(mode)
	}
	return nil
}

func (s *Session) SubscribePermissionMode(listener func(string)) (string, func()) {
	s.mu.Lock()
	if s.terminalPermission == nil {
		s.terminalPermission = &terminalPermissionState{}
	}
	state := s.terminalPermission
	mode := state.mode
	if listener == nil || s.closed {
		s.mu.Unlock()
		return mode, func() {}
	}
	state.sequence++
	id := state.sequence
	if state.listeners == nil {
		state.listeners = map[uint64]func(string){}
	}
	state.listeners[id] = listener
	s.mu.Unlock()
	var once sync.Once
	return mode, func() { once.Do(func() { s.mu.Lock(); delete(state.listeners, id); s.mu.Unlock() }) }
}
