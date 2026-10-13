package tui

import "time"

type DoctorPanel struct {
	Text   string
	Scroll int
}

func (s *State) handleDoctorKey(key Key, now time.Time) []action {
	switch key.Name {
	case "pageup", "wheelup":
		s.Doctor.Scroll = max(0, s.Doctor.Scroll-max(1, s.Height/2))
		return nil
	case "pagedown", "wheeldown":
		s.Doctor.Scroll += max(1, s.Height/2)
		return nil
	case "ctrl+home":
		s.Doctor.Scroll = 0
		return nil
	case "ctrl+end":
		s.Doctor.Scroll = len(wrap(s.Doctor.Text, s.Width))
		return nil
	}
	result := ResolveChord(keyStroke(key), []string{"Global", "Confirmation"}, s.bindings, s.pendingChord)
	switch result.Type {
	case "chord_started":
		s.pendingChord = result.Pending
		s.chordUntil = now.Add(time.Second)
		return nil
	case "match":
		s.pendingChord = nil
		switch result.Action {
		case "confirm:yes", "confirm:no":
			s.Doctor = nil
			s.Entries = append(s.Entries, Entry{Kind: "notice", Text: "Claude Code diagnostics dismissed"})
			return nil
		case "app:interrupt":
			key = Key{Name: "ctrl+c"}
		case "app:exit":
			key = Key{Name: "ctrl+d"}
		default:
			return nil
		}
	default:
		s.pendingChord = nil
		return nil
	}
	if key.Name == "ctrl+c" || key.Name == "ctrl+d" {
		if s.press(key.Name, now) {
			return s.EOF()
		}
		s.Status = "Press " + key.Name + " again to exit"
	}
	return nil
}
func (p *DoctorPanel) lines(width, height int) []string {
	lines := wrap(p.Text, width)
	available := max(1, height-4)
	p.Scroll = min(p.Scroll, max(0, len(lines)-available))
	end := min(len(lines), p.Scroll+available)
	out := append([]string{}, lines[p.Scroll:end]...)
	return append(out, wrap("PgUp/PgDn: scroll · Enter/Esc: dismiss", width)...)
}
