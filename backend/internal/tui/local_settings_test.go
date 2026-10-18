package tui

import (
	"strings"
	"testing"
	"time"
)

func TestConfigSearchFocusForwardCyclingRefreshAndNestedPicker(t *testing.T) {
	s := NewState(nil, 100, 36)
	s.Editor.Set("parent human draft")
	props := LocalPanel{ID: "config", Kind: "config", Title: "Config", Rows: []LocalPanelRow{{ID: "model", Label: "Model: source", SearchText: "Model", Actions: []LocalPanelAction{{Key: "right", Name: "edit-config"}}}, {ID: "autoCompactEnabled", Label: "Auto-compact: true", SearchText: "Auto-compact", Actions: []LocalPanelAction{{Key: "right", Name: "next-config"}}}, {ID: "theme", Label: "Theme: dark", SearchText: "Theme"}}}
	s.openLocalPanel(&props)
	now := time.Now()
	s.HandleKey(Key{Name: "/", Text: "/"}, now)
	s.Paste("AUTOcompact")
	if p := s.LocalPanel; p == nil || !p.searchFocused || len(p.props.Rows) != 1 || p.props.Rows[0].ID != "autoCompactEnabled" {
		t.Fatal(p)
	}
	s.HandleKey(Key{Name: "enter"}, now)
	if a := s.HandleKey(Key{Name: "left"}, now); len(a) != 1 || a[0].panel.Action != "next-config" || a[0].panel.Row != "autoCompactEnabled" {
		t.Fatal(a)
	}
	s.openLocalPanel(&props)
	if len(s.LocalPanel.props.Rows) != 1 || s.LocalPanel.settingsSearch.Text() != "AUTOcompact" {
		t.Fatal("refresh discarded search")
	}
	s.HandleKey(Key{Name: "up"}, now)
	s.HandleKey(Key{Name: "esc"}, now)
	if s.LocalPanel == nil || !s.LocalPanel.searchFocused || s.LocalPanel.settingsSearch.Text() != "" {
		t.Fatal("first search escape")
	}
	s.HandleKey(Key{Name: "esc"}, now)
	if s.LocalPanel == nil || s.LocalPanel.searchFocused {
		t.Fatal("second search escape")
	}
	s.HandleKey(Key{Text: "m", Name: "m"}, now)
	s.HandleKey(Key{Text: "o", Name: "o"}, now)
	s.HandleKey(Key{Text: "d", Name: "d"}, now)
	s.HandleKey(Key{Name: "down"}, now)
	if a := s.HandleKey(Key{Name: "tab"}, now); len(a) != 1 || a[0].panel.Action != "edit-config" {
		t.Fatal(a)
	}
	s.OpenModelPicker(ModelPicker{ID: "child", Options: []ModelOption{{Label: "actual option"}}})
	if !strings.Contains(s.View("", false).Content, "Select model") || strings.Contains(s.View("", false).Content, "Search settings") {
		t.Fatal(s.View("", false).Content)
	}
	if !s.terminalLocalUIActive() {
		t.Fatal("parent remained physically mounted during child")
	}
	s.HandleKey(Key{Name: "esc"}, now)
	s.openLocalPanel(&props)
	if s.LocalPanel.settingsSearch.Text() != "mod" || len(s.LocalPanel.props.Rows) != 1 || s.Editor.Text() != "parent human draft" {
		t.Fatal("nested child discarded parent state")
	}
}

func TestLocalPanelQueuedRefreshCannotReopenClosedOrSwitchedSettingsTab(t *testing.T) {
	s := NewState(nil, 100, 32)
	usage := LocalPanel{ID: "usage-1", Kind: "usage", Title: "Usage", Rows: []LocalPanelRow{{ID: "initial", Label: "Loading"}}}
	s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &usage})
	updated := usage
	updated.Rows = []LocalPanelRow{{ID: "loaded", Label: "Actual cached Usage result"}}
	s.Apply(Event{Kind: LocalPanelRefreshed, LocalPanel: &updated})
	if s.LocalPanel.props.Rows[0].ID != "loaded" {
		t.Fatal("active result was not consumed")
	}
	s.Apply(Event{Kind: LocalPanelClosed})
	s.Apply(Event{Kind: LocalPanelRefreshed, LocalPanel: &updated})
	if s.LocalPanel != nil {
		t.Fatal("queued result reopened a closed dialog")
	}
	config := LocalPanel{ID: "config-2", Kind: "config", Title: "Config"}
	s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &config})
	s.Apply(Event{Kind: LocalPanelRefreshed, LocalPanel: &updated})
	if s.LocalPanel.props.ID != "config-2" || s.LocalPanel.props.Kind != "config" {
		t.Fatal("queued result replaced the switched tab")
	}
	usage.ID = "usage-3"
	s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &usage})
	s.Apply(Event{Kind: LocalPanelRefreshed, LocalPanel: &updated})
	if s.LocalPanel.props.Rows[0].ID != "initial" {
		t.Fatal("earlier mount result replaced the new Usage mount")
	}
}

func TestConfigContentPickerSeedsSelectionRestoresParentAndRendersDescriptions(t *testing.T) {
	s := NewState(nil, 100, 32)
	props := LocalPanel{ID: "config", Kind: "config", Title: "Config", Rows: []LocalPanelRow{{ID: "one", Label: "One"}, {ID: "two", Label: "Two"}, {ID: "outputStyle", Label: "Output style"}}}
	s.openLocalPanel(&props)
	s.LocalPanel.selected = 2
	child := LocalPanel{ID: "config", Kind: "config", Title: "Preferred output style", Submenu: true, SelectedRow: "existing", RowDescriptions: true, VisibleRows: 10, Rows: []LocalPanelRow{{ID: "default", Label: "Default", Detail: "actual default description"}, {ID: "existing", Label: "Existing", Detail: "actual existing description"}}}
	s.openLocalPanel(&child)
	if s.LocalPanel.selected != 1 || !strings.Contains(strings.Join(s.LocalPanel.lines(100, 32), "\n"), "actual existing description") {
		t.Fatal(s.LocalPanel)
	}
	s.openLocalPanel(&props)
	if s.LocalPanel.selected != 2 {
		t.Fatal("child navigation changed parent selection", s.LocalPanel.selected)
	}
	s.LocalPanel.settingsSearch.Set("output")
	s.LocalPanel.filterSettingsRows()
	fieldChild := child
	fieldChild.Fields = []LocalPanelField{{Name: "language", Value: "Deutsch"}}
	s.openLocalPanel(&fieldChild)
	s.openLocalPanel(&props)
	if s.LocalPanel.settingsSearch.Text() != "output" || len(s.LocalPanel.props.Rows) != 1 || s.LocalPanel.props.Rows[0].ID != "outputStyle" {
		t.Fatal("input submenu discarded the mounted parent search")
	}
}
