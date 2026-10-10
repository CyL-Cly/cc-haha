package computeruse

import "strings"

var modifierAliases = map[string]string{
	"meta": "meta", "meta_l": "meta", "meta_r": "meta", "super": "meta", "super_l": "meta", "super_r": "meta", "command": "meta", "cmd": "meta", "windows": "meta", "win": "meta",
	"ctrl": "ctrl", "control": "ctrl", "control_l": "ctrl", "control_r": "ctrl", "lctrl": "ctrl", "lcontrol": "ctrl", "rctrl": "ctrl", "rcontrol": "ctrl",
	"shift": "shift", "shift_l": "shift", "shift_r": "shift", "lshift": "shift", "rshift": "shift", "alt": "alt", "alt_l": "alt", "alt_r": "alt", "option": "alt", "opt": "alt",
}
var keyAliases = map[string]string{"esc": "escape", "spacebar": "space", "del": "delete", "forwarddelete": "delete", "forward_delete": "delete", "deletef": "delete"}

func IsSystemKeyCombo(key, platform string) bool {
	blocked := []string{"ctrl+alt+delete", "alt+f4", "alt+tab", "meta+l", "meta+d"}
	if platform == "darwin" {
		blocked = []string{"meta+q", "shift+meta+q", "alt+meta+escape", "meta+tab", "meta+space", "ctrl+meta+q"}
	}
	for _, chord := range chords(key) {
		mods, keys := map[string]bool{}, map[string]bool{}
		for _, part := range strings.Split(strings.ToLower(chord), "+") {
			part = trim(part)
			if part == "" {
				continue
			}
			if canonical, found := modifierAliases[part]; found {
				mods[canonical] = true
			} else {
				if canonical, found := keyAliases[part]; found {
					part = canonical
				}
				keys[part] = true
			}
		}
		for _, entry := range blocked {
			parts := strings.Split(entry, "+")
			if !keys[parts[len(parts)-1]] {
				continue
			}
			matched := true
			for _, mod := range parts[:len(parts)-1] {
				if !mods[mod] {
					matched = false
				}
			}
			if matched {
				return true
			}
		}
	}
	return false
}
