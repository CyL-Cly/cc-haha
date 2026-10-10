package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"time"
)

func (s *Session) nativeFastModeNotification(value protocol.JSONObject) error {
	event := tui.Event{Kind: tui.Notice, Text: value.String("text"), IsError: value.String("color") == "warning", NoticeTimeout: 8 * time.Second}
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
	return nil
}
