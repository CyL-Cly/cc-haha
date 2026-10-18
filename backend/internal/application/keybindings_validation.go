package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

var terminalKeybindingContexts = []string{"Global", "Chat", "Autocomplete", "Confirmation", "Help", "Transcript", "HistorySearch", "Task", "ThemePicker", "Settings", "Tabs", "Attachments", "Footer", "MessageSelector", "DiffDialog", "ModelPicker", "Select", "Plugin"}
var terminalCommandBinding = regexp.MustCompile(`^command:[a-zA-Z0-9:\-_]+$`)

func terminalNormalizedKey(key string) string {
	steps := strings.FieldsFunc(protocol.TrimJavaScriptString(key), protocol.IsJavaScriptWhitespace)
	if len(steps) == 0 {
		steps = []string{""}
	}
	for i, step := range steps {
		modifiers, main := []string{}, ""
		for _, part := range strings.Split(step, "+") {
			part = strings.ToLower(protocol.TrimJavaScriptString(part))
			switch part {
			case "ctrl", "control", "alt", "opt", "option", "meta", "cmd", "command", "shift":
				if part == "control" {
					part = "ctrl"
				} else if part == "option" || part == "opt" {
					part = "alt"
				} else if part == "command" {
					part = "cmd"
				}
				modifiers = append(modifiers, part)
			default:
				main = part
			}
		}
		sort.Strings(modifiers)
		steps[i] = strings.Join(append(modifiers, main), "+")
	}
	return strings.Join(steps, " ")
}

func terminalRawDuplicateWarnings(content []byte) []terminalKeybindingWarning {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	warnings := []terminalKeybindingWarning{}
	var walk func(bool, string) error
	walk = func(bindings bool, context string) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if token == json.Delim('{') {
			seen := map[string]int{}
			for decoder.More() {
				property, err := decoder.Token()
				if err != nil {
					return err
				}
				name, _ := property.(string)
				seen[name]++
				if bindings && seen[name] == 2 {
					warnings = append(warnings, terminalKeybindingWarning{Type: "duplicate", Severity: "warning", Message: fmt.Sprintf(`Duplicate key "%s" in %s bindings`, name, context), Key: name, Context: context, Suggestion: "This key appears multiple times in the same context. JSON uses the last value, earlier values are ignored."})
				}
				if !bindings && name == "context" {
					var value json.RawMessage
					if err := decoder.Decode(&value); err != nil {
						return err
					}
					_ = json.Unmarshal(value, &context)
				} else if err := walk(name == "bindings", context); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
		if token == json.Delim('[') {
			for decoder.More() {
				if err := walk(false, "unknown"); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	_ = walk(false, "unknown")
	return warnings
}

func validateTerminalKeybindings(content []byte, blocks []protocol.JSONObject, platform string) []terminalKeybindingWarning {
	rawDuplicates := terminalRawDuplicateWarnings(content)
	warnings := []terminalKeybindingWarning{}
	valid := map[string]bool{}
	for _, context := range terminalKeybindingContexts {
		valid[context] = true
	}
	for _, block := range blocks {
		context, known := block.String("context"), valid[block.String("context")]
		if !known {
			warnings = append(warnings, terminalKeybindingWarning{Type: "invalid_context", Severity: "error", Message: fmt.Sprintf(`Unknown context "%s"`, context), Context: context, Suggestion: "Valid contexts: " + strings.Join(terminalKeybindingContexts, ", ")})
			context = ""
		}
		bindings, _ := block.Object("bindings")
		for _, property := range bindings.OwnProperties() {
			var key, action string
			_ = json.Unmarshal(property.NameJSON, &key)
			for _, part := range strings.Split(key, "+") {
				if protocol.TrimJavaScriptString(part) == "" {
					warnings = append(warnings, terminalKeybindingWarning{Type: "parse_error", Severity: "error", Message: fmt.Sprintf(`Empty key part in "%s"`, key), Key: key, Context: context, Suggestion: `Remove extra "+" characters`})
					break
				}
			}
			stroke := tui.ParseKeystroke(key)
			if stroke.Key == "" && !stroke.Ctrl && !stroke.Alt && !stroke.Shift && !stroke.Meta {
				warnings = append(warnings, terminalKeybindingWarning{Type: "parse_error", Severity: "error", Message: fmt.Sprintf(`Could not parse keystroke "%s"`, key), Key: key, Context: context})
			}
			if string(property.Value) == "null" {
				continue
			}
			if json.Unmarshal(property.Value, &action) != nil {
				warnings = append(warnings, terminalKeybindingWarning{Type: "invalid_action", Severity: "error", Message: fmt.Sprintf(`Invalid action for "%s": must be a string or null`, key), Key: key, Context: context})
			} else if strings.HasPrefix(action, "command:") {
				if !terminalCommandBinding.MatchString(action) {
					warnings = append(warnings, terminalKeybindingWarning{Type: "invalid_action", Severity: "warning", Message: fmt.Sprintf(`Invalid command binding "%s" for "%s": command name may only contain alphanumeric characters, colons, hyphens, and underscores`, action, key), Key: key, Context: context, Action: action})
				}
				if context != "" && context != "Chat" {
					warnings = append(warnings, terminalKeybindingWarning{Type: "invalid_action", Severity: "warning", Message: fmt.Sprintf(`Command binding "%s" must be in "Chat" context, not "%s"`, action, context), Key: key, Context: context, Action: action, Suggestion: `Move this binding to a block with "context": "Chat"`})
				}
			} else if action == "voice:pushToTalk" {
				first := tui.ParseChord(key)[0]
				if !first.Ctrl && !first.Alt && !first.Shift && !first.Meta && !first.Super && len(first.Key) == 1 && first.Key[0] >= 'a' && first.Key[0] <= 'z' {
					warnings = append(warnings, terminalKeybindingWarning{Type: "invalid_action", Severity: "warning", Message: fmt.Sprintf(`Binding "%s" to voice:pushToTalk prints into the input during warmup; use space or a modifier combo like meta+k`, key), Key: key, Context: context, Action: action})
				}
			}
		}
	}
	seenContext := map[string]map[string]string{}
	for _, block := range blocks {
		context := block.String("context")
		if seenContext[context] == nil {
			seenContext[context] = map[string]string{}
		}
		bindings, _ := block.Object("bindings")
		for _, property := range bindings.OwnProperties() {
			var key, action string
			_ = json.Unmarshal(property.NameJSON, &key)
			_ = json.Unmarshal(property.Value, &action)
			label := action
			if string(property.Value) == "null" {
				action, label = "null", "null (unbind)"
			}
			normalized := terminalNormalizedKey(key)
			if previous := seenContext[context][normalized]; previous != "" && previous != action {
				warnings = append(warnings, terminalKeybindingWarning{Type: "duplicate", Severity: "warning", Message: fmt.Sprintf(`Duplicate binding "%s" in %s context`, key, context), Key: key, Context: context, Action: label, Suggestion: fmt.Sprintf(`Previously bound to "%s". Only the last binding will be used.`, previous)})
			}
			seenContext[context][normalized] = action
		}
	}
	reserved := []struct{ key, reason, severity string }{
		{"ctrl+c", "Cannot be rebound - used for interrupt/exit (hardcoded)", "error"},
		{"ctrl+d", "Cannot be rebound - used for exit (hardcoded)", "error"},
		{"ctrl+m", "Cannot be rebound - identical to Enter in terminals (both send CR)", "error"},
		{"ctrl+z", "Unix process suspend (SIGTSTP)", "warning"},
		{`ctrl+\`, "Terminal quit signal (SIGQUIT)", "error"},
	}
	if platform == "macos" {
		for _, entry := range []struct{ key, reason string }{{"cmd+c", "macOS system copy"}, {"cmd+v", "macOS system paste"}, {"cmd+x", "macOS system cut"}, {"cmd+q", "macOS quit application"}, {"cmd+w", "macOS close window/tab"}, {"cmd+tab", "macOS app switcher"}, {"cmd+space", "macOS Spotlight"}} {
			reserved = append(reserved, struct{ key, reason, severity string }{entry.key, entry.reason, "error"})
		}
	}
	for _, block := range blocks {
		bindings, _ := block.Object("bindings")
		for _, property := range bindings.OwnProperties() {
			var key, action string
			_ = json.Unmarshal(property.NameJSON, &key)
			_ = json.Unmarshal(property.Value, &action)
			var chord []tui.Keystroke
			for _, step := range strings.Split(key, " ") {
				chord = append(chord, tui.ParseKeystroke(step))
			}
			display := tui.ChordString(chord)
			for _, entry := range reserved {
				if terminalNormalizedKey(display) == terminalNormalizedKey(entry.key) {
					warnings = append(warnings, terminalKeybindingWarning{Type: "reserved", Severity: entry.severity, Message: fmt.Sprintf(`"%s" may not work: %s`, display, entry.reason), Key: display, Context: block.String("context"), Action: action})
				}
			}
		}
	}
	filtered, seen := []terminalKeybindingWarning{}, map[string]bool{}
	for _, warning := range warnings {
		key := warning.Type + ":" + warning.Key + ":" + warning.Context
		if !seen[key] {
			seen[key] = true
			filtered = append(filtered, warning)
		}
	}
	return append(rawDuplicates, filtered...)
}
