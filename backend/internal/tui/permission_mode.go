package tui

import (
	"runtime"
	"strings"
)

func (s *State) modeCycleShortcut() string {
	platform := runtime.GOOS
	if platform == "darwin" {
		platform = "macos"
	}
	for i := len(s.bindings) - 1; i >= 0; i-- {
		binding := s.bindings[i]
		if binding.Action == nil || *binding.Action != "chat:cycleMode" || binding.Context != "Chat" || len(binding.Chord) == 0 {
			continue
		}
		var pending []Keystroke
		var result BindingResolution
		for _, key := range binding.Chord {
			result = ResolveChord(&key, []string{"Global", "Chat"}, s.bindings, pending)
			pending = result.Pending
		}
		if result.Type != "match" || result.Action != "chat:cycleMode" {
			continue
		}
		parts := make([]string, len(binding.Chord))
		for i, key := range binding.Chord {
			parts[i] = KeystrokeDisplay(key, platform)
		}
		return strings.Join(parts, " ")
	}
	return ""
}
