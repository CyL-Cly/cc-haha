package tui

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestLocalPanelOwnsNavigationFormAndDismissalWithoutPromptSubmission(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("draft preserved")
	s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &LocalPanel{ID: "panel", Title: "Permissions", Rows: []LocalPanelRow{{ID: "one", Label: "first", Detail: "first detail"}, {ID: "two", Label: "second", Detail: "second detail", Actions: []LocalPanelAction{{Key: "x", Name: "remove", Label: "remove"}}}}, Actions: []LocalPanelAction{{Key: "a", Name: "add", Label: "add"}}}})
	now := time.Now()
	s.HandleKey(Key{Name: "down"}, now)
	s.HandleKey(Key{Name: "enter"}, now)
	if text := strings.Join(s.LocalPanel.lines(80, 24), "\n"); !strings.Contains(text, "second detail") {
		t.Fatal(text)
	}
	actions := s.HandleKey(Key{Name: "x"}, now)
	if len(actions) != 1 || actions[0].kind != actionLocalPanel || actions[0].panel.Row != "two" || actions[0].panel.Action != "remove" {
		t.Fatal(actions)
	}
	s.HandleKey(Key{Name: "esc"}, now)
	if s.LocalPanel == nil || s.LocalPanel.detail {
		t.Fatal("detail Escape must return to list")
	}
	s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &LocalPanel{ID: "panel", Title: "Create", Fields: []LocalPanelField{{Name: "name", Label: "Name"}, {Name: "description", Label: "Description", Value: "old"}}}})
	for _, character := range "deny-worker-jk" {
		if actions := s.HandleKey(Key{Name: string(character), Text: string(character)}, now); len(actions) != 0 || s.LocalPanel == nil {
			t.Fatal("printable input escaped local editor", character, actions)
		}
	}
	s.HandleKey(Key{Name: "tab"}, now)
	s.HandleKey(Key{Name: "ctrl+u"}, now)
	s.Paste("private description")
	actions = s.HandleKey(Key{Name: "enter"}, now)
	if len(actions) != 1 || actions[0].panel.Fields["name"] != "deny-worker-jk" || actions[0].panel.Fields["description"] != "private description" || actions[0].panel.Action != "save" {
		t.Fatal(actions)
	}
	if len(s.Queue) != 0 || len(s.Entries) != 0 || s.Editor.Text() != "draft preserved" {
		t.Fatal("local form changed conversation", s.Queue, s.Entries, s.Editor.Text())
	}
	actions = s.HandleKey(Key{Name: "esc"}, now)
	if s.LocalPanel != nil || len(actions) != 1 || actions[0].panel.Action != "close" {
		t.Fatal(actions)
	}
	s.Apply(Event{Kind: LocalPanelClosed})
}

func TestTodoCyclePreviewAndDoublePressAgentStopUseActualTaskKinds(t *testing.T) {
	s := NewState(nil, 90, 25)
	now := time.Now()
	for _, expected := range []string{"tasks", "none"} {
		s.HandleKey(Key{Name: "ctrl+t"}, now)
		if s.Teammates.ExpandedView != expected {
			t.Fatal(s.Teammates.ExpandedView)
		}
	}
	var tasks protocol.JSONObject
	_ = tasks.UnmarshalJSON([]byte(`{"t":{"id":"t","type":"in_process_teammate","status":"running","description":"actual teammate","identity":{"agentName":"worker"},"messages":[{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"running teammate preview"}]}}]},"a":{"id":"a","type":"local_agent","status":"running","description":"actual detached agent"},"b":{"id":"b","type":"local_bash","status":"running"}}`))
	teammate, _ := tasks.Object("t")
	teammate.Set("startTime", mustLocalMessageJSON(now.Add(-time.Second).UnixMilli()))
	s.SetTeammateTasks(tasks)
	for _, expected := range []string{"tasks", "teammates", "none"} {
		s.HandleKey(Key{Name: "ctrl+t"}, now)
		if s.Teammates.ExpandedView != expected {
			t.Fatal(s.Teammates.ExpandedView)
		}
	}
	s.Apply(Event{Kind: TeammateMessages, Result: []byte(`[{"from":"worker","summary":"delivered summary","text":"full message"}]`)})
	for _, expected := range []string{"tasks", "teammates"} {
		s.HandleKey(Key{Name: "ctrl+t"}, now)
		if s.Teammates.ExpandedView != expected {
			t.Fatal(s.Teammates.ExpandedView)
		}
	}
	if view := ansi.Strip(s.View("", false).Content); s.ShowTeammatePreview || !strings.Contains(view, "delivered summary") || strings.Contains(view, "running teammate preview") || strings.Contains(view, "full message") {
		t.Fatal("Delivered message summary and disabled running-task preview must remain separate", view)
	}
	s.HandleKey(Key{Name: "ctrl+shift+o"}, now)
	if view := ansi.Strip(s.View("", false).Content); !s.ShowTeammatePreview || !strings.Contains(view, "delivered summary") || !strings.Contains(view, "running teammate preview") || strings.Contains(view, "full message") {
		t.Fatal("Preview shortcut must show running-task rows and retain delivered summary", view)
	}
	s.Transcript = true
	if !strings.Contains(strings.Join(s.transcriptLines(), "\n"), "full message") {
		t.Fatal("transcript must retain full teammate")
	}
	s.Transcript = false
	s.Queue = []Input{{Text: "must discard"}}
	press := func(at time.Time) []action {
		s.HandleKey(Key{Name: "ctrl+x"}, at)
		return s.HandleKey(Key{Name: "ctrl+k"}, at)
	}
	if actions := press(now); len(actions) != 0 || !strings.Contains(s.Notification, "again") {
		t.Fatal(actions, s.Notification)
	}
	if actions := press(now.Add(2 * time.Second)); len(actions) != 1 || actions[0].name != "chat:killAgents" || len(s.Queue) != 0 {
		t.Fatal(actions, s.Queue)
	}
	tasks.Delete("a")
	s.SetTeammateTasks(tasks)
	if actions := press(now.Add(3 * time.Second)); len(actions) != 0 || s.Notification != "No background agents running" {
		t.Fatal(actions, s.Notification)
	}
}

func TestLocalPanelEmptyWindowSelectionAndFormEditingBoundaries(t *testing.T) {
	s := NewState(nil, 12, 8)
	now := time.Now()
	s.openLocalPanel(&LocalPanel{ID: "empty", Title: "Empty", Notice: "notice"})
	for _, name := range []string{"up", "down", "enter", "unknown"} {
		if actions := s.HandleKey(Key{Name: name}, now); len(actions) > 0 {
			t.Fatal(actions)
		}
	}
	if lines := strings.Join(s.LocalPanel.lines(12, 8), "\n"); !strings.Contains(lines, "No entries") {
		t.Fatal(lines)
	}
	s.openLocalPanel(&LocalPanel{ID: "edit", Fields: []LocalPanelField{{Name: "first", Value: "abc"}, {Name: "second", Value: "z"}}})
	for _, name := range []string{"left", "backspace", "delete", "home", "right", "end", "ctrl+a", "ctrl+e", "shift+tab", "up", "down", "tab"} {
		s.HandleKey(Key{Name: name}, now)
	}
	s.HandleKey(Key{Text: "中文"}, now)
	if result := s.LocalPanel.result("save"); result.Fields["first"] != "a中文" {
		t.Fatal(result)
	}
	s.openLocalPanel(nil)
	if s.LocalPanel != nil {
		t.Fatal("nil open")
	}
}
