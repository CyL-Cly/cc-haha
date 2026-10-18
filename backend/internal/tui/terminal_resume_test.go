package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalResumeLiteralRenameSearchCancelAndCustomKeyContexts(t *testing.T) {
	raw := json.RawMessage(`{"logs":[{"sessionId":"22222222-2222-4222-8222-222222222222","customTitle":"owned resume parent","firstPrompt":"old prompt","gitBranch":"topic","modified":1790812801000,"fileSize":900}],"currentDirectory":"/private/fixture","currentBranch":"topic","hasMultipleWorktrees":false}`)
	s := NewState(nil, 100, 36)
	s.Editor.Set("preserved normal draft")
	s.openLocalPanel(&LocalPanel{ID: "actual-menu", Kind: "resume", Presentation: raw})
	now := time.UnixMilli(1790812802000)
	s.HandleKey(Key{Name: "ctrl+r"}, now)
	if s.LocalPanel.resumeUI().mode != "rename" {
		t.Fatal("rename did not mount")
	}
	for _, text := range []string{"renamed", " ", "north", " ", "yes"} {
		if a := s.HandleKey(Key{Text: text}, now); len(a) != 0 {
			t.Fatal("literal rename triggered action", text, a)
		}
	}
	result := s.HandleKey(Key{Name: "enter"}, now)
	if len(result) != 1 || result[0].panel.Action != "resume-rename" || result[0].panel.Fields["title"] != "renamed north yes" {
		t.Fatal("actual typed title lost", result)
	}
	s.HandleKey(Key{Text: "owned"}, now)
	if s.LocalPanel.resumeUI().mode != "search" {
		t.Fatal("typing did not mount search")
	}
	s.HandleKey(Key{Name: "esc"}, now)
	if s.LocalPanel.resumeUI().mode != "search" || s.LocalPanel.resumeUI().search.Text() != "" {
		t.Fatal("Source first Esc clears only nonempty search")
	}
	s.HandleKey(Key{Name: "esc"}, now)
	if s.LocalPanel.resumeUI().mode != "list" {
		t.Fatal("second Esc exits empty search")
	}
	s.bindings = append(s.bindings, binding("Select", "alt+q", "select:cancel"), Keybinding{Context: "Select", Chord: ParseChord("escape")})
	s.HandleKey(Key{Name: "esc"}, now)
	if s.LocalPanel == nil {
		t.Fatal("unbound original cancellation key ignored override")
	}
	a := s.HandleKey(Key{Name: "alt+q", Stroke: func() *Keystroke { x := ParseKeystroke("alt+q"); return &x }()}, now)
	if len(a) != 1 || a[0].panel.Action != "resume-cancel" || s.Editor.Text() != "preserved normal draft" {
		t.Fatal("actual cancel/draft", a, s.Editor.Text())
	}
}
func TestTerminalResumeFilterAlphabeticalTagsAndFullWidthNumericSelection(t *testing.T) {
	view := protocol.NewJSONObject(protocol.JSONField{Name: "logs", Value: json.RawMessage(`[{"sessionId":"one","customTitle":"İstanbul","tag":"é","gitBranch":"topic"},{"sessionId":"two","customTitle":"other","tag":"a","gitBranch":"other"}]`)}, protocol.JSONField{Name: "currentBranch", Value: json.RawMessage(`"topic"`)})
	data, _ := view.MarshalJSON()
	s := NewState(nil, 100, 36)
	s.openLocalPanel(&LocalPanel{ID: "filter", Kind: "resume", Presentation: data})
	p := s.LocalPanel
	if tags := terminalResumeTags(p.resumeLogs()); len(tags) != 2 || tags[0] != "a" {
		t.Fatal("Source actual ICU tag order", tags)
	}
	s.HandleKey(Key{Name: "ctrl+b"}, time.Now())
	if rows := p.visibleTerminalResume(); len(rows) != 1 || rows[0] != 0 {
		t.Fatal(rows)
	}
	s.HandleKey(Key{Text: "i"}, time.Now())
	if rows := p.visibleTerminalResume(); len(rows) != 1 || !strings.Contains(lowerJS(resumeSourceDisplayTitle(p.resumeLogs()[0])), "i") {
		t.Fatal(rows)
	}
	s.HandleKey(Key{Name: "enter"}, time.Now())
	a := s.HandleKey(Key{Text: "１"}, time.Now())
	if len(a) != 1 || a[0].panel.Action != "resume-select" || a[0].panel.Row != "0" {
		t.Fatal(a)
	}
}

func TestTerminalResumeRenameReloadUnmountsSearchButOrdinaryRefreshRetainsIt(t *testing.T) {
	s := NewState(nil, 100, 36)
	panel := &LocalPanel{ID: "actual-rename", Kind: "resume", Presentation: json.RawMessage(`{"logs":[{"sessionId":"owned","customTitle":"old target"}]}`)}
	s.openLocalPanel(panel)
	s.HandleKey(Key{Text: "old target"}, time.Now())
	s.openLocalPanel(panel)
	if s.LocalPanel.resumeUI().search.Text() != "old target" {
		t.Fatal("ordinary same-mount refresh discarded the current query")
	}
	s.openLocalPanel(&LocalPanel{ID: panel.ID, Kind: "resume", Presentation: json.RawMessage(`{"loading":true}`)})
	s.openLocalPanel(&LocalPanel{ID: panel.ID, Kind: "resume", Presentation: json.RawMessage(`{"logs":[{"sessionId":"owned","customTitle":"renamed target"}]}`)})
	if s.LocalPanel.resumeUI().search.Text() != "" || s.LocalPanel.resumeUI().mode != "list" || len(s.LocalPanel.visibleTerminalResume()) != 1 {
		t.Fatal("actual loading unmount retained the old search and hid the renamed session")
	}
}

func TestTerminalResumePreviewConfirmationAndFreshRemountKeepDistinctConsumers(t *testing.T) {
	s := NewState(nil, 100, 36)
	s.openLocalPanel(&LocalPanel{ID: "actual-preview", Kind: "resume", Presentation: json.RawMessage(`{"logs":[{"sessionId":"owned","customTitle":"target"}],"preview":{"messages":[{"type":"user","message":{"role":"user","content":"OLD_PREVIEW"}}]}}`)})
	opened := s.HandleKey(Key{Name: "ctrl+v"}, time.Now())
	if len(opened) != 1 || opened[0].panel.Action != "resume-preview" || s.LocalPanel.resumeView().Get("preview") != nil {
		t.Fatal("new preview mount reused its old full-log projection", opened)
	}
	confirmed := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(confirmed) != 1 || confirmed[0].panel.Action != "resume-preview-select" || confirmed[0].panel.Row != "0" {
		t.Fatal("preview confirmation lost its captured-log consumer", confirmed)
	}
	s.HandleKey(Key{Name: "esc"}, time.Now())
	selected := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(selected) != 1 || selected[0].panel.Action != "resume-select" {
		t.Fatal("ordinary list selection acquired the old preview capability", selected)
	}
}
