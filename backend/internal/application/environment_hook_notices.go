package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"sync"
	"time"
)

func (s *Session) SubscribeEnvironmentHookNotices(listener func(tui.Event)) func() {
	s.mu.Lock()
	if listener == nil || s.closed {
		s.mu.Unlock()
		return func() {}
	}
	s.environmentNoticeID++
	id := s.environmentNoticeID
	if s.environmentNotices == nil {
		s.environmentNotices = map[uint64]func(tui.Event){}
	}
	s.environmentNotices[id] = listener
	s.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { s.mu.Lock(); delete(s.environmentNotices, id); s.mu.Unlock() }) }
}

func (s *Session) environmentHookNotice(text string, isError bool) {
	timeout := 5 * time.Second
	if isError {
		timeout = 8 * time.Second
	}
	event := tui.Event{Kind: tui.Notice, Text: text, IsError: isError, NoticeTimeout: timeout}
	s.mu.Lock()
	listeners := []func(tui.Event){}
	if !s.closed {
		for _, listener := range s.environmentNotices {
			listeners = append(listeners, listener)
		}
	}
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(event)
	}
}
