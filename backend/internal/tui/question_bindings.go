package tui

import "time"

func (s *State) handleQuestionBindings(key Key, now time.Time) []action {
	q := s.questionFor(s.Permissions[0])
	if q.err != nil {
		return s.handleQuestionKey(key)
	}
	contexts := []string{"Global", "Select"}
	if !q.inputFocused() {
		contexts = append(contexts, "Tabs")
	} else {
		contexts = append(contexts, "Chat")
	}
	resolved := ResolveChord(keyStroke(key), contexts, s.bindings, s.pendingChord)
	switch resolved.Type {
	case "chord_started":
		s.pendingChord = resolved.Pending
		s.chordUntil = now.Add(time.Second)
		return nil
	case "chord_cancelled", "unbound":
		s.pendingChord = nil
		return nil
	case "match":
		s.pendingChord = nil
		if resolved.Action == "chat:externalEditor" && q.inputFocused() {
			s.Editing = true
			return []action{{kind: actionEditor, permission: q.id, questionIndex: q.index, text: q.editors[q.index].Text()}}
		}
		switch resolved.Action {
		case "tabs:next":
			if q.index < len(q.questions) {
				q.index++
			}
			return nil
		case "tabs:previous":
			q.index = max(0, q.index-1)
			return nil
		case "select:previous":
			key = Key{Name: "up"}
		case "select:next":
			key = Key{Name: "down"}
		case "select:accept":
			key = Key{Name: "enter"}
		case "select:cancel":
			key = Key{Name: "esc"}
		default:
			if name, ok := nativeActionKey(resolved.Action); ok {
				key = Key{Name: name}
			}
		}
	}
	return s.handleQuestionKey(key)
}
