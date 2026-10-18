package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The task objects are inputs to the original mounted AppState consumer.
// This fixture contains no elapsed-time, subscription, or keyed-ref logic.
func terminalTeammateLifetimeRows() []map[string]any {
	const base int64 = 1700000000000
	member := func(id, name string, idle bool) map[string]any {
		color := "blue"
		if id == "beta" {
			color = "red"
		}
		return map[string]any{"id": id, "type": "in_process_teammate", "status": "running", "isIdle": idle, "identity": map[string]any{"agentName": name, "agentId": id + "@owned", "teamName": "owned", "color": color}, "startTime": base - 40000, "totalPausedMs": 2500, "spinnerVerb": "Reading", "pastTenseVerb": "Worked", "progress": map[string]any{"tokenCount": 1234, "toolUseCount": 1}, "messages": []any{}}
	}
	clone := func(value map[string]any) map[string]any {
		raw, _ := json.Marshal(value)
		var copy map[string]any
		_ = json.Unmarshal(raw, &copy)
		return copy
	}
	initial := map[string]any{"alpha": member("alpha", "Alpha", false), "beta": member("beta", "Beta", true)}
	both := clone(initial)
	both["alpha"].(map[string]any)["isIdle"] = true
	reorder := clone(both)
	reorder["alpha"].(map[string]any)["identity"].(map[string]any)["agentName"] = "Zulu"
	beta := reorder["beta"].(map[string]any)
	beta["identity"].(map[string]any)["agentName"] = "Able"
	beta["spinnerVerb"], beta["pastTenseVerb"], beta["startTime"] = "Searching", "Searched", base-100000
	active := clone(reorder)
	active["alpha"].(map[string]any)["isIdle"] = false
	completed := clone(active)
	completed["beta"].(map[string]any)["status"] = "completed"
	remountedIdle := clone(active)
	remountedIdle["alpha"].(map[string]any)["isIdle"] = true
	steps := []map[string]any{
		{"teammateTick": 1000}, {"background": both}, {"advanceWallClock": 5000}, {"background": reorder},
		{"appState": map[string]any{"viewSelectionMode": "selecting-agent", "selectedIPAgentIndex": 0}},
		{"appState": map[string]any{"viewSelectionMode": "viewing-agent", "viewingAgentTaskId": "beta"}},
		{"background": active}, {"teammateTick": 1000}, {"appState": map[string]any{"expandedView": "none"}},
		{"advanceWallClock": 5000}, {"appState": map[string]any{"expandedView": "teammates", "viewSelectionMode": "viewing-agent"}, "removeState": []string{"viewingAgentTaskId"}},
		{"teammateTick": 1000}, {"background": completed}, {"teammateTick": 1000}, {"background": active}, {"background": remountedIdle}, {"advanceWallClock": 5000},
	}
	rows := []map[string]any{}
	for _, profile := range []struct {
		theme string
		width int
	}{{"dark", 100}, {"light-ansi", 24}} {
		rows = append(rows, map[string]any{"tasks": []any{}, "background": initial, "expanded": "teammates", "theme": profile.theme, "width": profile.width, "height": 36, "loading": true, "message": "Teammate clock", "sourceMessage": true, "responseLength": 4000, "clockConsumer": true, "clockBase": base, "teammateClock": true, "streamConsumer": true, "providerClock": true, "global": map[string]any{"btwUseCount": 1, "showExpandedTodos": false, "showExpandedIPAgents": true}, "steps": steps})
	}
	return rows
}

func terminalTeammateLifetimeModel(ctx context.Context, row map[string]any) *terminalModel {
	s := NewState(nil, row["width"].(int), 36)
	s.Preferences = TerminalPreferences{Theme: row["theme"].(string), ReducedMotion: true}
	s.ActiveTurn, s.Status = "teammate-clock-query", "Teammate clock…"
	s.dreamClock = time.UnixMilli(row["clockBase"].(int64))
	clock := s.ensureTerminalSpinnerClock()
	clock.verb, clock.verbKnown, clock.responseUnits, clock.loading, clock.started = "Teammate clock", true, 4000, true, s.dreamClock
	raw, _ := json.Marshal(row["background"])
	s.SetTeammateTasks(presentationObject(raw))
	s.Teammates.ExpandedView = "teammates"
	return &terminalModel{ctx: ctx, state: s}
}

// Use an actual command/message from the mounted model. Only the OS timestamp
// may be replaced; the private mount/generation identity is copied unchanged.
// An old consumer with no independent row subscription fails a business
// assertion without needing a production type that does not exist yet.
func terminalTeammateLifetimeTick(ctx context.Context, command tea.Cmd, at time.Time) (tea.Msg, error) {
	if command == nil {
		return nil, fmt.Errorf("mounted idle row did not register an independent 1000ms Tea command")
	}
	result := make(chan tea.Msg, 1)
	go func() {
		var visit func(tea.Cmd) tea.Msg
		visit = func(cmd tea.Cmd) tea.Msg {
			if cmd == nil {
				return nil
			}
			msg := cmd()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, child := range batch {
					if found := visit(child); found != nil {
						return found
					}
				}
				return nil
			}
			if msg != nil && reflect.TypeOf(msg).Name() == "terminalTeammateSpinnerTickMsg" {
				return msg
			}
			return nil
		}
		result <- visit(command)
	}()
	select {
	case msg := <-result:
		if msg == nil {
			return nil, fmt.Errorf("mounted idle row returned no independent 1000ms clock notification")
		}
		copy := reflect.New(reflect.TypeOf(msg)).Elem()
		copy.Set(reflect.ValueOf(msg))
		now := copy.FieldByName("Now")
		if !now.IsValid() || !now.CanSet() || now.Type() != reflect.TypeOf(at) {
			return nil, fmt.Errorf("actual row clock does not expose its OS timestamp primitive")
		}
		now.Set(reflect.ValueOf(at))
		return copy.Interface(), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func terminalTeammateLifetimeRegion(s *State, frame terminalSpinnerSourceFrame) []string {
	view := strings.Split(s.View("", false).Content, "\n")
	first := ""
	for _, line := range frame.Rows {
		if strings.TrimSpace(line) != "" {
			first = line
			break
		}
	}
	for index, line := range view {
		if strings.TrimRight(ansi.Strip(line), " ") == first {
			if index > 0 && strings.TrimSpace(ansi.Strip(view[index-1])) == "" {
				index--
			}
			return view[index:min(len(view), index+len(frame.PaintRows))]
		}
	}
	return nil
}

func TestActualMountedSourceTeammateSpinnerIndependentTicksAndKeyedLifetimes(t *testing.T) {
	rows := terminalTeammateLifetimeRows()
	original := terminalSpinnerOriginalFrames(t, true, rows)
	for profile, row := range rows {
		t.Run(fmt.Sprint(profile), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			m := terminalTeammateLifetimeModel(ctx, row)
			frames := original[profile]
			_, command := m.Update(tea.WindowSizeMsg{Width: m.state.Width, Height: m.state.Height})
			at := m.state.dreamClock
			for index, frame := range frames {
				if index > 0 {
					step := row["steps"].([]map[string]any)[index-1]
					if delta, ok := step["teammateTick"].(int); ok {
						at = at.Add(time.Duration(delta) * time.Millisecond)
						m.state.dreamClock = at
						// Completed removal has no Source subscriber and no callback.
						if index != 14 {
							parent := m.state.ensureTerminalSpinnerClock().parentRevision
							msg, err := terminalTeammateLifetimeTick(ctx, command, at)
							if err != nil {
								t.Fatal(err)
							}
							_, command = m.Update(msg)
							if m.state.ensureTerminalSpinnerClock().parentRevision != parent {
								t.Fatal("row timer committed the parent")
							}
						}
					} else if delta, ok := step["advanceWallClock"].(int); ok {
						at = at.Add(time.Duration(delta) * time.Millisecond)
						m.state.dreamClock = at
					} else if background, ok := step["background"]; ok {
						raw, _ := json.Marshal(background)
						_, command = m.Update(teammatesChangedMsg{presentationObject(raw)})
					} else if props, ok := step["appState"].(map[string]any); ok {
						// This is the same ordinary AppState prop change as the
						// original fixture. Timer notifications never take this path.
						if value, ok := props["expandedView"].(string); ok {
							m.state.Teammates.ExpandedView = value
						}
						if value, ok := props["viewSelectionMode"].(string); ok {
							m.state.Teammates.ViewSelectionMode = value
						}
						if value, ok := props["selectedIPAgentIndex"].(int); ok {
							m.state.Teammates.SelectedIPAgentIndex = value
						}
						if value, ok := props["viewingAgentTaskId"].(string); ok {
							m.state.Teammates.ViewingAgentTaskID = value
						}
						if _, ok := step["removeState"]; ok {
							m.state.Teammates.ViewingAgentTaskID = ""
						}
						_, command = m.Update(tea.WindowSizeMsg{Width: m.state.Width, Height: m.state.Height})
					}
				}
				got := terminalTeammateLifetimeRegion(m.state, frame)
				if !reflect.DeepEqual(terminalTaskListCells(frame.PaintRows, m.state.Width), terminalTaskListCells(got, m.state.Width)) {
					t.Errorf("whole mounted Source width%d step%d cells/styles: Source%q Go%q fullView%q", m.state.Width, index, frame.Rows, got, m.state.View("", false).Content)
				}
			}
		})
	}
}

func TestTerminalTeammateSpinnerClockRetiresOnRemovalAndClose(t *testing.T) {
	row := terminalTeammateLifetimeRows()[0]
	for _, ending := range []string{"hide", "remove", "close"} {
		t.Run(ending, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			m := terminalTeammateLifetimeModel(ctx, row)
			m.state.View("", false)
			_, command := m.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
			if command == nil {
				t.Fatal("actual mounted idle row has no owned clock command to retire")
			}
			// The timestamp is the only controlled primitive. This notification
			// carries the identity from the actual old mounted command.
			late, err := terminalTeammateLifetimeTick(ctx, command, m.state.dreamClock.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			_, pending := m.Update(late)
			if pending == nil {
				t.Fatal("live row did not resubscribe after its actual notification")
			}
			// Observe the actual command blocked in its own timer wait before
			// retiring it. The identity is obtained from this returned closure,
			// not from a test callback or a constructed business revision.
			code := runtime.FuncForPC(reflect.ValueOf(pending).Pointer()).Name()
			done := make(chan tea.Msg, 1)
			go func() { done <- pending() }()
			deadline := time.Now().Add(2 * time.Second)
			held := false
			for !held && time.Now().Before(deadline) {
				buffer := make([]byte, 1<<20)
				trace := string(buffer[:runtime.Stack(buffer, true)])
				for _, goroutine := range strings.Split(trace, "\n\n") {
					if strings.Contains(goroutine, code) && (strings.Contains(goroutine, "[select]") || strings.Contains(goroutine, "[chan receive]")) {
						held = true
					}
				}
				if !held {
					time.Sleep(time.Millisecond)
				}
			}
			if !held {
				t.Fatal("actual row command never entered its owned timer wait", code)
			}
			switch ending {
			case "hide":
				m.state.Teammates.ExpandedView = "none"
			case "remove":
				raw, _ := json.Marshal(map[string]any{"alpha": row["background"].(map[string]any)["alpha"]})
				m.state.SetTeammateTasks(presentationObject(raw))
			case "close":
				m.state.Closing = true
				cancel()
			}
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
			select {
			case message := <-done:
				if message != nil {
					t.Fatal("retired timer produced another row notification", ending, code, message)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("actual held row timer did not cancel/join", ending, code)
			}
			before := m.state.View("", false).Content
			parent := m.state.ensureTerminalSpinnerClock().parentRevision
			m.Update(late)
			if after := m.state.View("", false).Content; after != before || m.state.ensureTerminalSpinnerClock().parentRevision != parent {
				t.Fatal("retired row callback changed the closed/removed mount")
			}
			if strings.Contains(before, "Idle for") {
				t.Fatal("retired row remained visible", before)
			}
		})
	}
}
