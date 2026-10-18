package tui

import (
	"strings"
	"testing"
	"time"
)

func TestMCPMountedDialogsReserveCtrlCDForDoublePressExit(t *testing.T) {
	for _, scope := range []string{"list", "server:stdio", "server:http", "tools:stdio", "tool:stdio", "agent:owned"} {
		for _, key := range []string{"ctrl+c", "ctrl+d"} {
			t.Run(scope+key, func(t *testing.T) {
				s := NewState(nil, 80, 24)
				now := time.Now()
				panel := LocalPanel{ID: "actual-panel", Kind: "mcp", Title: scope, MCPExitScope: scope}
				s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &panel})
				s.Editor.Set("next human draft")
				if actions := s.HandleKey(Key{Name: key}, now); len(actions) != 0 || s.LocalPanel == nil || s.Closing {
					t.Fatal("first MCP Ctrl-C/D dismissed the actual dialog", actions, s.LocalPanel, s.Closing)
				}
				// A refresh of the same mounted component retains its hook refs.
				s.Apply(Event{Kind: LocalPanelRefreshed, LocalPanel: &panel})
				actions := s.HandleKey(Key{Name: key}, now.Add(100*time.Millisecond))
				if !s.Closing || len(actions) == 0 || actions[len(actions)-1].kind != actionQuit || s.Editor.Text() != "next human draft" {
					t.Fatal("second MCP exit was not consumed by actual terminal lifecycle", actions, s.Closing)
				}
			})
		}
	}
	s := NewState(nil, 80, 24)
	now := time.Now()
	panel := LocalPanel{ID: "panel", Kind: "mcp", Title: "first", MCPExitScope: "first"}
	s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &panel})
	s.HandleKey(Key{Name: "ctrl+c"}, now)
	if s.Notification != "" || !strings.Contains(strings.Join(s.LocalPanel.lines(80, 24), "\n"), "Press Ctrl-C again to exit") {
		t.Fatal("MCP exit reminder escaped its mounted footer", s.Notification)
	}
	s.HandleKey(Key{Name: "ctrl+d"}, now.Add(100*time.Millisecond))
	if s.Closing {
		t.Fatal("different Ctrl keys share a double-press timer")
	}
	s.HandleKey(Key{Name: "ctrl+c"}, now.Add(900*time.Millisecond))
	if s.Closing {
		t.Fatal("expired first press exited")
	}
	panel.Title, panel.MCPExitScope = "second", "second"
	s.Apply(Event{Kind: LocalPanelRefreshed, LocalPanel: &panel})
	if strings.Contains(strings.Join(s.LocalPanel.lines(80, 24), "\n"), "again to exit") {
		t.Fatal("unmounted MCP footer retained the old exit reminder")
	}
	s.HandleKey(Key{Name: "ctrl+c"}, now.Add(time.Second))
	if s.Closing {
		t.Fatal("newly mounted MCP component retained old timer")
	}
	actions := s.HandleKey(Key{Name: "esc"}, now.Add(1100*time.Millisecond))
	if s.LocalPanel != nil || len(actions) != 1 || actions[0].panel.Action != "close" {
		t.Fatal("MCP Esc no longer dismisses", actions)
	}
	if strings.Contains(s.Editor.Text(), "ctrl") {
		t.Fatal("control input reached model draft")
	}
}
