package tui

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func binding(context, key, action string) Keybinding {
	return Keybinding{Context: context, Chord: ParseChord(key), Action: &action}
}

func TestConfiguredBindingOverridesUnbindsAndUsesModalContext(t *testing.T) {
	s := NewState([]Completion{{Name: "model"}}, 80, 24)
	bindings := DefaultKeybindings("macos", true, KeybindingFeatures{})
	bindings = append(bindings, binding("Chat", "alt+u", "chat:undo"), Keybinding{Context: "Chat", Chord: ParseChord("ctrl+s")}, binding("Confirmation", "alt+y", "confirm:yes"))
	s.SetKeybindings(bindings)
	// The host cannot change the UI's copies after publishing a reload.
	bindings[len(bindings)-1].Chord[0].Key = "z"
	s.Editor.Insert("中文")
	s.Editor.Insert("👋")
	key(s, "alt+u")
	if s.Editor.Text() != "中文" {
		t.Fatal("custom undo was not consumed by editor", s.Editor.Text())
	}
	key(s, "ctrl+s")
	if s.Editor.Text() != "中文" || s.stash != "" {
		t.Fatal("null binding ran the old physical shortcut")
	}
	s.Permissions = []PermissionRequest{{ID: "p", ToolName: "Write"}}
	actions := key(s, "alt+y")
	if len(actions) != 1 || actions[0].kind != actionPermission || !actions[0].decision.Allowed || actions[0].permission != "p" {
		t.Fatal("modal custom binding did not reach original permission", actions)
	}
}

func TestNativeChordsHonorOverridesCancelAndTimeout(t *testing.T) {
	now := time.Unix(100, 0)
	s := NewState(nil, 80, 24)
	s.Editor.Set("draft")
	if actions := s.HandleKey(Key{Name: "ctrl+x"}, now); len(actions) != 0 || len(s.pendingChord) != 1 {
		t.Fatal("Source chord prefix was not retained")
	}
	if actions := s.HandleKey(Key{Name: "ctrl+e"}, now.Add(999*time.Millisecond)); len(actions) != 1 || actions[0].kind != actionEditor || actions[0].text != "draft" {
		t.Fatal("editor chord did not consume draft", actions)
	}
	s.Editing = false
	s.HandleKey(Key{Name: "ctrl+x"}, now)
	if actions := s.HandleKey(Key{Name: "ctrl+e"}, now.Add(time.Second)); len(actions) != 0 || s.Editing {
		t.Fatal("expired chord executed external editor")
	}
	if s.Editor.Cursor() != len("draft") {
		t.Fatal("post-timeout readline input lost")
	}
	s.HandleKey(Key{Name: "ctrl+x"}, now.Add(2*time.Second))
	s.HandleKey(Key{Name: "z", Text: "z"}, now.Add(2100*time.Millisecond))
	if s.Editor.Text() != "draft" || s.pendingChord != nil {
		t.Fatal("invalid chord key propagated into draft")
	}
	bindings := []Keybinding{binding("Chat", "ctrl+x", "chat:stash"), binding("Chat", "ctrl+x ctrl+e", "chat:externalEditor"), {Context: "Chat", Chord: ParseChord("ctrl+x ctrl+e")}}
	s.SetKeybindings(bindings)
	if actions := key(s, "ctrl+x"); len(actions) != 0 || s.Editor.Text() != "" || s.stash != "draft" || s.pendingChord != nil {
		t.Fatal("null chord shadow did not release the prefix key", actions)
	}
}

func TestHostActionsKeepDraftAndDoNotSubmitCommandPrompt(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("unsubmitted 中文👋")
	s.SetKeybindings([]Keybinding{binding("Chat", "ctrl+k ctrl+m", "command:model"), binding("Chat", "alt+p", "chat:modelPicker")})
	key(s, "ctrl+k")
	actions := key(s, "ctrl+m")
	if len(actions) != 1 || actions[0].kind != actionInvoke || actions[0].name != "command:model" {
		t.Fatal("command binding bypassed shared action consumer", actions)
	}
	if s.Editor.Text() != "unsubmitted 中文👋" || s.Busy() || len(s.history) != 0 {
		t.Fatal("command binding cleared or submitted draft")
	}
	actions = key(s, "alt+p")
	if len(actions) != 1 || actions[0].name != "chat:modelPicker" {
		t.Fatal("model picker not delegated to host", actions)
	}
}

func TestPhysicalPageKeyAdapterUsesOriginalScrollKeystrokesAndModifiers(t *testing.T) {
	for _, row := range []struct {
		code rune
		name string
	}{{tea.KeyPgUp, "pageup"}, {tea.KeyPgDown, "pagedown"}, {tea.KeyKpPgUp, "pageup"}, {tea.KeyKpPgDown, "pagedown"}} {
		for _, mod := range []tea.KeyMod{0, tea.ModAlt, tea.ModCtrl, tea.ModShift} {
			actual := terminalKey(tea.KeyPressMsg{Code: row.code, Mod: mod})
			stroke := keyStroke(actual)
			if stroke == nil || stroke.Key != row.name || stroke.Alt != mod.Contains(tea.ModAlt) || stroke.Ctrl != mod.Contains(tea.ModCtrl) || stroke.Shift != mod.Contains(tea.ModShift) {
				t.Fatal("physical terminal Page key differs from Source Scroll context", actual, stroke, row, mod)
			}
		}
	}
}

func TestModifierAdapterSeparatesSuperAndAltAndRejectsExtendedInput(t *testing.T) {
	alt := terminalKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModAlt})
	super := terminalKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModSuper})
	if !keystrokesEqual(*keyStroke(alt), ParseKeystroke("meta+k")) || keystrokesEqual(*keyStroke(alt), *keyStroke(super)) {
		t.Fatal("terminal modifiers collapsed distinct super", alt, super)
	}
	escape := terminalKey(tea.KeyPressMsg{Code: tea.KeyEscape, Mod: tea.ModAlt})
	if stroke := keyStroke(escape); stroke == nil || stroke.Alt || stroke.Meta {
		t.Fatal("legacy escape meta quirk changed", escape)
	}
	if keyStroke(Key{Name: "👋", Text: "👋"}) != nil || keyStroke(Key{Name: "ab", Text: "ab"}) != nil {
		t.Fatal("Source UTF16 input length was widened")
	}
}

func TestBindingJSONKeepsJavaScriptEnumerationAndExactSlashSpaces(t *testing.T) {
	bindings, err := ParseBindingBlocks(json.RawMessage(`[{"context":"Chat","bindings":{"z":"chat:stash","2":"two","1":"one","z":null,"ctrl+a ctrl+b":"command:model"}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 4 || bindings[0].Chord[0].Key != "1" || bindings[1].Chord[0].Key != "2" || bindings[2].Chord[0].Key != "z" || bindings[2].Action != nil {
		t.Fatal("own-key enumeration or last duplicate changed", bindings)
	}
	cases := map[string]*ParsedSlashCommand{
		"\ufeff /mcp:tool (MCP) a  b\u3000": {CommandName: "mcp:tool (MCP)", IsMCP: true, Args: "a  b"},
		"/model  deepseek":                  {CommandName: "model", Args: " deepseek"},
		"/model\tdeepseek":                  {CommandName: "model\tdeepseek"},
		"/tool  (MCP) a":                    {CommandName: "tool", Args: " (MCP) a"},
		"/":                                 nil, "/ model": nil, "ordinary text": nil,
	}
	for input, want := range cases {
		if got := ParseSlashCommand(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("slash %q got %#v want %#v", input, got, want)
		}
	}
}
