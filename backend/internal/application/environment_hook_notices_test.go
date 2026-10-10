package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"testing"
	"time"
)

func TestIdleEnvironmentHookNoticesUseOriginalTimeoutErrorAndUnsubscribe(t *testing.T) {
	session := &Session{}
	events := []tui.Event{}
	unsubscribe := session.SubscribeEnvironmentHookNotices(func(event tui.Event) { events = append(events, event) })
	session.environmentHookNotice("actual informational environment hook", false)
	session.environmentHookNotice("actual failed environment hook", true)
	if len(events) != 2 || events[0].Kind != tui.Notice || events[0].NoticeTimeout != 5*time.Second || events[0].IsError || events[1].NoticeTimeout != 8*time.Second || !events[1].IsError {
		t.Fatal(events)
	}
	unsubscribe()
	unsubscribe()
	session.environmentHookNotice("after unsubscribe", true)
	if len(events) != 2 {
		t.Fatal(events)
	}
}
