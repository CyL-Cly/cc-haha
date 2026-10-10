package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Keystroke preserves the Source distinction between terminal alt/meta and
// kitty-protocol super. Config spelling is retained for canonical display.
type Keystroke struct {
	Key   string `json:"key"`
	Ctrl  bool   `json:"ctrl"`
	Alt   bool   `json:"alt"`
	Shift bool   `json:"shift"`
	Meta  bool   `json:"meta"`
	Super bool   `json:"super"`
}

type Keybinding struct {
	Chord   []Keystroke `json:"chord"`
	Action  *string     `json:"action"`
	Context string      `json:"context"`
}

type BindingResolution struct {
	Type    string      `json:"type"`
	Action  string      `json:"action,omitempty"`
	Pending []Keystroke `json:"pending,omitempty"`
}

func jsSpace(r rune) bool {
	return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}

func trimJS(value string) string  { return strings.TrimFunc(value, jsSpace) }
func lowerJS(value string) string { return cases.Lower(language.Und).String(value) }

func ParseKeystroke(input string) Keystroke {
	var key Keystroke
	for _, part := range strings.Split(input, "+") {
		switch lower := lowerJS(part); lower {
		case "ctrl", "control":
			key.Ctrl = true
		case "alt", "opt", "option":
			key.Alt = true
		case "shift":
			key.Shift = true
		case "meta":
			key.Meta = true
		case "cmd", "command", "super", "win":
			key.Super = true
		case "esc":
			key.Key = "escape"
		case "return":
			key.Key = "enter"
		case "space":
			key.Key = " "
		case "↑":
			key.Key = "up"
		case "↓":
			key.Key = "down"
		case "←":
			key.Key = "left"
		case "→":
			key.Key = "right"
		default:
			key.Key = lower
		}
	}
	return key
}

func ParseChord(input string) []Keystroke {
	if input == " " {
		return []Keystroke{ParseKeystroke("space")}
	}
	parts := strings.FieldsFunc(trimJS(input), jsSpace)
	if len(parts) == 0 {
		parts = []string{""} // JavaScript ''.split(/\s+/) is [''].
	}
	result := make([]Keystroke, len(parts))
	for i, part := range parts {
		result[i] = ParseKeystroke(part)
	}
	return result
}

func displayKey(key string) string {
	switch key {
	case "escape":
		return "Esc"
	case " ":
		return "Space"
	case "enter":
		return "Enter"
	case "backspace":
		return "Backspace"
	case "delete":
		return "Delete"
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "left":
		return "←"
	case "right":
		return "→"
	case "pageup":
		return "PageUp"
	case "pagedown":
		return "PageDown"
	case "home":
		return "Home"
	case "end":
		return "End"
	default:
		return key
	}
}

func KeystrokeString(key Keystroke) string {
	var parts []string
	if key.Ctrl {
		parts = append(parts, "ctrl")
	}
	if key.Alt {
		parts = append(parts, "alt")
	}
	if key.Shift {
		parts = append(parts, "shift")
	}
	if key.Meta {
		parts = append(parts, "meta")
	}
	if key.Super {
		parts = append(parts, "cmd")
	}
	return strings.Join(append(parts, displayKey(key.Key)), "+")
}

func KeystrokeDisplay(key Keystroke, platform string) string {
	var parts []string
	if key.Ctrl {
		parts = append(parts, "ctrl")
	}
	if key.Alt || key.Meta {
		modifier := "alt"
		if platform == "macos" {
			modifier = "opt"
		}
		parts = append(parts, modifier)
	}
	if key.Shift {
		parts = append(parts, "shift")
	}
	if key.Super {
		modifier := "super"
		if platform == "macos" {
			modifier = "cmd"
		}
		parts = append(parts, modifier)
	}
	return strings.Join(append(parts, displayKey(key.Key)), "+")
}

func ChordString(chord []Keystroke) string {
	parts := make([]string, len(chord))
	for i, key := range chord {
		parts[i] = KeystrokeString(key)
	}
	return strings.Join(parts, " ")
}

// ParseBindingBlocks receives the original JSON blocks, preserving JS own-key
// enumeration and null unbindings. Loading, gates and watching belong to the
// interactive host; this parser never discovers a user's configuration path.
func ParseBindingBlocks(raw json.RawMessage) ([]Keybinding, error) {
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	bindings := make([]Keybinding, 0)
	for i, block := range blocks {
		var context string
		if err := json.Unmarshal(block.Get("context"), &context); err != nil {
			return nil, fmt.Errorf("keybinding block %d context: %w", i+1, err)
		}
		object, ok := block.Object("bindings")
		if !ok {
			return nil, fmt.Errorf("keybinding block %d has no bindings object", i+1)
		}
		for _, property := range object.OwnProperties() {
			var key string
			if err := json.Unmarshal(property.NameJSON, &key); err != nil {
				return nil, err
			}
			var action *string
			if err := json.Unmarshal(property.Value, &action); err != nil {
				return nil, err
			}
			bindings = append(bindings, Keybinding{Chord: ParseChord(key), Action: action, Context: context})
		}
	}
	return bindings, nil
}

func keystrokesEqual(a, b Keystroke) bool {
	return a.Key == b.Key && a.Ctrl == b.Ctrl && a.Shift == b.Shift && (a.Alt || a.Meta) == (b.Alt || b.Meta) && a.Super == b.Super
}

func chordMatches(a, b []Keystroke) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if !keystrokesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func activeBindings(bindings []Keybinding, contexts []string) []Keybinding {
	set := make(map[string]bool, len(contexts))
	for _, context := range contexts {
		set[context] = true
	}
	var result []Keybinding
	for _, binding := range bindings {
		if set[binding.Context] {
			result = append(result, binding)
		}
	}
	return result
}

func ResolveKey(current *Keystroke, contexts []string, bindings []Keybinding) BindingResolution {
	if current == nil {
		return BindingResolution{Type: "none"}
	}
	if current.Key == "escape" {
		copy := *current
		copy.Alt, copy.Meta = false, false
		current = &copy
	}
	var winner *Keybinding
	for _, binding := range activeBindings(bindings, contexts) {
		if len(binding.Chord) == 1 && keystrokesEqual(*current, binding.Chord[0]) {
			copy := binding
			winner = &copy
		}
	}
	return bindingWinner(winner)
}

func bindingWinner(winner *Keybinding) BindingResolution {
	if winner == nil {
		return BindingResolution{Type: "none"}
	}
	if winner.Action == nil {
		return BindingResolution{Type: "unbound"}
	}
	return BindingResolution{Type: "match", Action: *winner.Action}
}

func ResolveChord(current *Keystroke, contexts []string, bindings []Keybinding, pending []Keystroke) BindingResolution {
	if current != nil && current.Key == "escape" && pending != nil {
		return BindingResolution{Type: "chord_cancelled"}
	}
	if current == nil {
		if pending != nil {
			return BindingResolution{Type: "chord_cancelled"}
		}
		return BindingResolution{Type: "none"}
	}
	copy := *current
	copy.Alt, copy.Meta = current.Alt || current.Meta, current.Alt || current.Meta
	if copy.Key == "escape" {
		copy.Alt, copy.Meta = false, false
	}
	current = &copy
	chord := append(append([]Keystroke(nil), pending...), *current)
	bindings = activeBindings(bindings, contexts)
	winners := map[string]*string{}
	for _, binding := range bindings {
		if len(binding.Chord) > len(chord) && chordMatches(chord, binding.Chord) {
			winners[ChordString(binding.Chord)] = binding.Action
		}
	}
	for _, action := range winners {
		if action != nil {
			return BindingResolution{Type: "chord_started", Pending: chord}
		}
	}
	var winner *Keybinding
	for _, binding := range bindings {
		if len(binding.Chord) == len(chord) && chordMatches(chord, binding.Chord) {
			copy := binding
			winner = &copy
		}
	}
	if winner != nil {
		return bindingWinner(winner)
	}
	if pending != nil {
		return BindingResolution{Type: "chord_cancelled"}
	}
	return BindingResolution{Type: "none"}
}

func BindingDisplay(action, context string, bindings []Keybinding) (string, bool) {
	for i := len(bindings) - 1; i >= 0; i-- {
		b := bindings[i]
		if b.Action != nil && *b.Action == action && b.Context == context {
			return ChordString(b.Chord), true
		}
	}
	return "", false
}

func keyStroke(key Key) *Keystroke {
	if key.Stroke != nil {
		copy := *key.Stroke
		return &copy
	}
	stroke := ParseKeystroke(key.Name)
	switch stroke.Key {
	case "escape", "enter", "tab", "backspace", "delete", "up", "down", "left", "right", "pageup", "pagedown", "wheelup", "wheeldown", "home", "end":
	default:
		if len(utf16.Encode([]rune(stroke.Key))) != 1 {
			return nil
		}
	}
	if stroke.Key == "escape" {
		stroke.Alt, stroke.Meta = false, false
	}
	return &stroke
}
