package tui

import (
	"testing"
	"time"
)

func TestTerminalPluginNotificationsInitialCachedStatusKeysPrioritiesAndOriginalTimeout(t *testing.T) {
	now := time.Now()
	s := NewState(nil, 100, 30)
	s.applyNotice(Event{Text: "Existing ordinary notice", NoticeTimeout: time.Second})
	s.applyTerminalPluginInstallationStatus([]byte(`{"flaggedPlugins":["sample@mkt"],"needsRefresh":true}`), now)
	if s.Notification != "Existing ordinary notice" || len(s.pluginNotices.queue) != 2 {
		t.Fatal("ordinary current notification must not be preempted", s.Notification, s.pluginNotices)
	}
	s.Tick(now.Add(time.Second + time.Millisecond))
	if s.pluginNotices.current != "plugin-delisted-flagged" || s.Notification != "Plugins flagged. Check /plugins" || len(s.pluginNotices.queue) != 1 {
		t.Fatal("Source high-priority flagged notice", s.Notification, s.pluginNotices)
	}
	deadline := s.notificationUntil
	s.applyTerminalPluginInstallationStatus([]byte(`{"flaggedPlugins":["sample@mkt"],"needsRefresh":false}`), now.Add(2*time.Second))
	s.applyTerminalPluginInstallationStatus([]byte(`{"flaggedPlugins":["sample@mkt"],"needsRefresh":true}`), now.Add(3*time.Second))
	if len(s.pluginNotices.queue) != 1 || !s.notificationUntil.Equal(deadline) {
		t.Fatal("duplicate key must not extend current or duplicate queued notification")
	}
	s.Tick(deadline.Add(-time.Nanosecond))
	if s.pluginNotices.current != "plugin-delisted-flagged" {
		t.Fatal("original default timeout fired early")
	}
	s.Tick(deadline)
	if s.pluginNotices.current != "plugin-reload-pending" || s.Notification != "Plugins changed. Run /reload-plugins to activate." || !s.notificationUntil.Equal(deadline.Add(8*time.Second)) {
		t.Fatal("Source queued suggestion / exact 8-second timeout", s.Notification, s.pluginNotices)
	}
	s.applyTerminalPluginInstallationStatus([]byte(`{"flaggedPlugins":[],"needsRefresh":false}`), deadline)
	if s.Notification == "" {
		t.Fatal("Source false effect does not explicitly remove existing notification")
	}
	s.Tick(deadline.Add(8 * time.Second))
	if s.Notification != "" || s.pluginNotices.current != "" || len(s.pluginNotices.queue) != 0 {
		t.Fatal("original timeout did not retire current/queue")
	}
	s.applyTerminalPluginInstallationStatus([]byte(`{"flaggedPlugins":["sample@mkt"],"needsRefresh":false}`), deadline.Add(9*time.Second))
	if s.Notification != "" {
		t.Fatal("flagged notification is a mount concern, not repeated on every status publication")
	}
}
