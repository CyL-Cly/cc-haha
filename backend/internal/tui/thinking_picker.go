package tui

import (
	"context"
	"time"
)

type ThinkingPicker struct {
	ID                       string
	Current, MidConversation bool
}
type ThinkingPickerResult struct{ Cancelled, Enabled bool }
type ThinkingPickerSession interface {
	RespondThinkingPicker(context.Context, string, ThinkingPickerResult, Emit) error
}
type thinkingPickerState struct {
	props    ThinkingPicker
	selected int
	pending  *bool
}

func (s *State) OpenThinkingPicker(props ThinkingPicker) {
	selected := 0
	if !props.Current {
		selected = 1
	}
	s.ThinkingPicker = &thinkingPickerState{props: props, selected: selected}
	s.pendingChord = nil
}
func (s *State) handleThinkingPickerKey(key Key, now time.Time) []action {
	p := s.ThinkingPicker
	if p == nil {
		return nil
	}
	close := func(result ThinkingPickerResult) []action {
		s.ThinkingPicker = nil
		return []action{{kind: actionThinkingPicker, permission: p.props.ID, thinkingPickerResult: result}}
	}
	switch key.Name {
	case "alt+t":
		s.ThinkingPicker = nil
		return close(ThinkingPickerResult{Cancelled: true})
	case "esc":
		if p.pending != nil {
			p.pending = nil
			return nil
		}
		return close(ThinkingPickerResult{Cancelled: true})
	case "ctrl+c", "ctrl+d":
		if s.press(key.Name, now) {
			return s.EOF()
		}
		s.Status = "Press " + key.Name + " again to exit"
		return nil
	case "up", "down", "j", "k", "ctrl+n", "ctrl+p":
		if p.pending == nil {
			p.selected = 1 - p.selected
		}
		return nil
	case "enter":
		enabled := p.selected == 0
		if p.pending != nil {
			return close(ThinkingPickerResult{Enabled: *p.pending})
		}
		if p.props.MidConversation && enabled != p.props.Current {
			p.pending = &enabled
			return nil
		}
		return close(ThinkingPickerResult{Enabled: enabled})
	}
	return nil
}
func (p *thinkingPickerState) lines(width int) []string {
	lines := wrap("Toggle thinking mode\nEnable or disable thinking for this session.", width)
	if p.pending != nil {
		return append(lines, wrap("Changing thinking mode mid-conversation will increase latency and may reduce quality. For best results, set this at the start of a session.\nDo you want to proceed?\nEnter: confirm · Esc: cancel", width)...)
	}
	for i, option := range []string{"Enabled · Claude will think before responding", "Disabled · Claude will respond without extended thinking"} {
		prefix := "  "
		if i == p.selected {
			prefix = "> "
		}
		lines = append(lines, wrap(prefix+option, width)...)
	}
	return append(lines, wrap("Enter: confirm · Esc: exit", width)...)
}

func (s *State) handleThinkingPickerBindings(key Key, now time.Time) []action {
	contexts := []string{"Global", "Select", "Confirmation"}
	if s.ThinkingPicker.pending != nil {
		contexts = []string{"Global", "Confirmation"}
	}
	result := ResolveChord(keyStroke(key), contexts, s.bindings, s.pendingChord)
	switch result.Type {
	case "unbound", "chord_cancelled":
		s.pendingChord = nil
		return nil
	case "chord_started":
		s.pendingChord = result.Pending
		s.chordUntil = now.Add(time.Second)
		return nil
	case "match":
		s.pendingChord = nil
		switch result.Action {
		case "select:previous":
			key = Key{Name: "up"}
		case "select:next":
			key = Key{Name: "down"}
		case "select:accept", "confirm:yes":
			key = Key{Name: "enter"}
		case "select:cancel", "confirm:no":
			key = Key{Name: "esc"}
		case "app:interrupt":
			key = Key{Name: "ctrl+c"}
		case "app:exit":
			key = Key{Name: "ctrl+d"}
		default:
			return nil
		}
	}
	return s.handleThinkingPickerKey(key, now)
}
