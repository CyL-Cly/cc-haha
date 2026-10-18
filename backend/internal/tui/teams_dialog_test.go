package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type terminalTeamsMountTestSession struct {
	*testSession
	retired map[string]bool
	called  bool
}

func (s *terminalTeamsMountTestSession) RetireTerminalTeams(id string) { s.retired[id] = true }
func (s *terminalTeamsMountTestSession) OpenTerminalTeams(_ context.Context, request TerminalTeamsRequest, emit Emit) error {
	s.called = true
	if s.retired[request.ID] {
		return context.Canceled
	}
	return nil
}

func TestTerminalTeamsCommittedCloseRetiresQueuedOpenAndLateProjection(t *testing.T) {
	session := &terminalTeamsMountTestSession{testSession: &testSession{}, retired: map[string]bool{}}
	m := &terminalModel{ctx: context.Background(), session: session, state: NewState(nil, 100, 35)}
	m.state.CachedTeams = []protocol.JSONObject{cachedTeam("owned", 1)}
	cmd := m.terminalTeamsCommand()
	id := m.state.LocalPanel.props.ID
	if id == "" || m.state.LocalPanel.props.Kind != "teams" {
		t.Fatal("No actual synchronous modal mount")
	}
	m.state.Apply(Event{Kind: LocalPanelClosed})
	m.syncTerminalTeamsMount()
	if !session.retired[id] {
		t.Fatal("Committed close did not retire pending app mount")
	}
	if msg := cmd(); msg != nil {
		t.Fatal("Retired open created visible cancellation message", msg)
	}
	if !session.called {
		t.Fatal("Actual typed Session boundary not called")
	}
	m.state.Apply(Event{Kind: LocalPanelRefreshed, LocalPanel: &LocalPanel{ID: id, Kind: "teams"}})
	if m.state.LocalPanel != nil {
		t.Fatal("Retired refresh reopened old Teams modal")
	}
	m.state.openLocalPanel(&LocalPanel{ID: "next-owned", Kind: "teams"})
	m.state.Apply(Event{Kind: LocalPanelClosed, LocalPanel: &LocalPanel{ID: id, Kind: "teams"}})
	if m.state.LocalPanel == nil || m.state.LocalPanel.props.ID != "next-owned" {
		t.Fatal("Old completed mount closed its replacement")
	}
	m.turns.Wait()
}

func TestTerminalTeamsKeysContextPromptBackResetAndLiveCallbackClamp(t *testing.T) {
	s := NewState(nil, 100, 35)
	list := &LocalPanel{ID: "owned", Kind: "teams", Rows: []LocalPanelRow{{ID: "a"}, {ID: "b"}, {ID: "c"}}, Presentation: json.RawMessage(`{"level":"list","supportsHideShow":false}`)}
	s.openLocalPanel(list)
	s.LocalPanel.selected = 2
	a := s.HandleKey(Key{Name: "k", Text: "k"}, time.Now())
	if len(a) != 1 || a[0].panel.Action != "teams-kill" || a[0].panel.Row != "c" {
		t.Fatal(a)
	}
	list.Presentation = json.RawMessage(`{"level":"list","selectionRevision":1,"selectionMaximum":1}`)
	s.openLocalPanel(list)
	if s.LocalPanel.selected != 1 {
		t.Fatal("Real successful async callback did not clamp live index")
	}
	a = s.HandleKey(Key{Name: "h", Text: "h"}, time.Now())
	if len(a) != 0 {
		t.Fatal("Unavailable hide/show falsely ran backend action")
	}
	list.Presentation = json.RawMessage(`{"level":"detail","memberName":"b"}`)
	s.openLocalPanel(list)
	a = s.HandleKey(Key{Name: "p", Text: "p"}, time.Now())
	if len(a) != 0 || !s.LocalPanel.teamsPromptExpanded {
		t.Fatal("Detail p incorrectly pruned actual roster")
	}
	s.openLocalPanel(list)
	if !s.LocalPanel.teamsPromptExpanded {
		t.Fatal("Ordinary same-member refresh remounted Prompt toggle")
	}
	a = s.HandleKey(Key{Name: "left"}, time.Now())
	if len(a) != 1 || a[0].panel.Action != "teams-back" {
		t.Fatal(a)
	}
	list.Presentation = json.RawMessage(`{"level":"list"}`)
	s.openLocalPanel(list)
	if s.LocalPanel.selected != 0 {
		t.Fatal("Original goBackToList must reset index0")
	}
	custom, err := ParseBindingBlocks([]byte(`[{"context":"Confirmation","bindings":{"ctrl+k m":"confirm:cycleMode","shift+tab":null}}]`))
	if err != nil {
		t.Fatal(err)
	}
	s.SetKeybindings(append(DefaultKeybindings("macos", false, KeybindingFeatures{}), custom...))
	if a = s.HandleKey(Key{Name: "ctrl+k"}, time.Now()); len(a) != 0 {
		t.Fatal(a)
	}
	a = s.HandleKey(Key{Name: "m", Text: "m"}, time.Now())
	if len(a) != 1 || a[0].panel.Action != "teams-cycle-mode" {
		t.Fatal("Custom Confirmation chord lost actual mode consumer", a)
	}
}

func TestTerminalTeamsRenderedDetailNameAndShrunkListRemainActionAuthorities(t *testing.T) {
	s := NewState(nil, 100, 35)
	panel := &LocalPanel{ID: "owned", Kind: "teams", Rows: []LocalPanelRow{{ID: "a"}, {ID: "b"}, {ID: "c"}}, Presentation: json.RawMessage(`{"level":"detail","memberName":"three","members":[{"name":"one"},{"name":"two"},{"name":"three"}]}`)}
	s.openLocalPanel(panel)
	s.LocalPanel.selected = 2
	s.HandleKey(Key{Name: "down"}, time.Now())
	if s.LocalPanel.selected != 0 {
		t.Fatal("Source detail navigation did not update the independent list index")
	}
	actions := s.HandleKey(Key{Name: "k", Text: "k"}, time.Now())
	if len(actions) != 1 || actions[0].panel.TeamsIndex != 2 || actions[0].panel.Row != "c" || actions[0].panel.TeamsLevel != "detail" {
		t.Fatal("Detail action stopped targeting its displayed member", actions)
	}
	panel.Presentation = json.RawMessage(`{"level":"list"}`)
	s.openLocalPanel(panel)
	s.LocalPanel.selected = 2
	panel.Rows = panel.Rows[:1]
	s.openLocalPanel(panel)
	if s.LocalPanel.selected != 2 {
		t.Fatal("Periodic list refresh incorrectly clamped the Source retained index")
	}
	for _, key := range []Key{{Name: "enter"}, {Name: "k", Text: "k"}, {Name: "s", Text: "s"}} {
		if a := s.HandleKey(key, time.Now()); len(a) != 0 {
			t.Fatal("Missing selected member acquired action authority", a)
		}
	}
	actions = s.HandleKey(Key{Name: "shift+tab"}, time.Now())
	if len(actions) != 1 || actions[0].panel.Action != "teams-cycle-mode" || actions[0].panel.Row != "" {
		t.Fatal("Retained out-of-range list index prevented actual bulk cycle", actions)
	}
	encoded, err := json.Marshal(actions[0].panel)
	if err != nil || strings.Contains(string(encoded), "Teams") {
		t.Fatal("Internal rendered-frame authority leaked into serialized panel result", string(encoded), err)
	}
}
