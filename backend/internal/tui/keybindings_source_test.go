package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

type bindingEvent struct {
	Current  *Keystroke  `json:"current"`
	Contexts []string    `json:"contexts"`
	Pending  []Keystroke `json:"pending"`
}

func TestActualSourceKeybindingsAndSlashParsing(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source comparison requires explicitly supplied Bun 1.3.14")
	}
	source, err := filepath.Abs("testdata/keybindings.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []struct {
		platform string
		features []string
		flags    KeybindingFeatures
	}{
		{"macos", nil, KeybindingFeatures{}},
		{"windows", nil, KeybindingFeatures{}},
		{"linux", []string{"KAIROS", "QUICK_SEARCH", "TERMINAL_PANEL", "MESSAGE_ACTIONS", "VOICE_MODE"}, KeybindingFeatures{Kairos: true, QuickSearch: true, TerminalPanel: true, MessageActions: true, VoiceMode: true}},
	} {
		t.Run(profile.platform, func(t *testing.T) {
			allocation := t.TempDir()
			home := filepath.Join(allocation, "home")
			for _, leaf := range []string{"", "tmp", "config", "cache"} {
				if err := os.MkdirAll(filepath.Join(home, leaf), 0700); err != nil {
					t.Fatal(err)
				}
			}
			keys := []string{"ctrl+shift+k", "CONTROL+opt+↑", "cmd+Space", "win+left", "meta+alt+Esc", "", "ctrl++", "Ctrl +K", "İ", "ΣΣ", "A+B", "shift+😀", "return", "option+pageup", "super+home"}
			chords := []string{" ", "", " ctrl+x\tctrl+e ", "\ufeffalt+K\u00a0meta+s\u3000", "cmd+space ctrl+k", "\u0085", "ctrl+x  ctrl+k", "ctrl+😀"}
			blocks := json.RawMessage(`[{"context":"Global","bindings":{"ctrl+k":"global","ctrl+x":"single","ctrl+x ctrl+e":"editor","alt+k":"alt","meta+k":"meta","cmd+k":"super","escape":"exit","1":"one","0":"zero"}},{"context":"Chat","bindings":{"ctrl+k":"chat","ctrl+x ctrl+e":null,"ctrl+x ctrl+s":"save","ctrl+x ctrl+s ctrl+m":"nested","ctrl+x ctrl+s ctrl+m":null,"shift+k":"shift","中":"chinese","😀":"emoji","ctrl+u":null}},{"context":"Confirmation","bindings":{"ctrl+k":"confirm","ctrl+x ctrl+s":null}}]`)
			bindings, err := ParseBindingBlocks(blocks)
			if err != nil {
				t.Fatal(err)
			}
			var events []bindingEvent
			for _, contexts := range [][]string{{"Global"}, {"Chat", "Global"}, {"Confirmation", "Chat", "Global"}, {"Missing"}} {
				for _, key := range []string{"ctrl+k", "alt+k", "meta+k", "cmd+k", "ctrl+x", "ctrl+s", "escape", "shift+k", "中", "😀", "ctrl+u", "q"} {
					current := ParseKeystroke(key)
					for _, pending := range [][]Keystroke{nil, ParseChord("ctrl+x"), ParseChord("ctrl+x ctrl+s")} {
						events = append(events, bindingEvent{&current, contexts, pending})
					}
				}
			}
			slashes := []string{"/model  deepseek", "/tool (MCP) a  b", "/tool  (MCP) a", "\ufeff /tool\targ\u3000", "/", "/ x", "text", "/cmd\narg", "/💬 a", "\u0085/model", "/cmd a\u00a0"}
			raw, _ := json.Marshal(map[string]any{"home": home, "platform": profile.platform, "keys": keys, "chords": chords, "blocks": blocks, "events": events, "slashes": slashes})
			args := []string{"--no-env-file"}
			for _, feature := range profile.features {
				args = append(args, "--feature="+feature)
			}
			args = append(args, source)
			cmd := exec.Command(bun, args...)
			cmd.Dir = home
			cmd.Stdin = bytes.NewReader(raw)
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "USER_TYPE=external", "ANTHROPIC_API_KEY=fixture", "CC_HAHA_KEYBINDING_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil || stderr.Len() != 0 {
				t.Fatalf("Source failed %v: %s", err, stderr.String())
			}
			var want struct {
				Template string
				Keys     []struct {
					Parsed             Keystroke
					Canonical, Display string
				}
				Chords []struct {
					Parsed    []Keystroke
					Canonical string
				}
				Bindings, Defaults []Keybinding
				Events             []struct{ Single, Chord BindingResolution }
				Displays           []*string
				Slashes            []*ParsedSlashCommand
				Capabilities       struct{ Blocked, SecureReads []any }
			}
			if err := json.Unmarshal(output, &want); err != nil {
				t.Fatal(err, string(output))
			}
			if len(want.Capabilities.Blocked) != 0 || len(want.Capabilities.SecureReads) != 0 {
				t.Fatal("Source escaped fixture", string(output))
			}
			for i, key := range keys {
				got := ParseKeystroke(key)
				if got != want.Keys[i].Parsed || KeystrokeString(got) != want.Keys[i].Canonical || KeystrokeDisplay(got, profile.platform) != want.Keys[i].Display {
					t.Fatalf("key %q got %#v/%q Source %#v", key, got, KeystrokeString(got), want.Keys[i])
				}
			}
			for i, chord := range chords {
				got := ParseChord(chord)
				if !reflect.DeepEqual(got, want.Chords[i].Parsed) || ChordString(got) != want.Chords[i].Canonical {
					t.Fatalf("chord %q got %#v Source %#v", chord, got, want.Chords[i])
				}
			}
			if !reflect.DeepEqual(bindings, want.Bindings) {
				t.Fatal("ordered parseBindings differs", bindings, want.Bindings)
			}
			if got := DefaultKeybindings(profile.platform, true, profile.flags); !reflect.DeepEqual(got, want.Defaults) {
				t.Fatalf("Source default bindings differ\nGo: %#v\nSource: %#v", got, want.Defaults)
			}
			if got, err := KeybindingsTemplate(profile.platform, true, profile.flags); err != nil || got != want.Template {
				t.Fatalf("actual Source keybindings template differs: %v\nGo: %s\nSource: %s", err, got, want.Template)
			}
			for i, event := range events {
				current := keyStroke(Key{Name: KeystrokeString(*event.Current)})
				if got := ResolveKey(current, event.Contexts, bindings); !reflect.DeepEqual(got, want.Events[i].Single) {
					t.Fatalf("single %d %#v got %#v Source %#v", i, event, got, want.Events[i].Single)
				}
				if got := ResolveChord(current, event.Contexts, bindings, event.Pending); !reflect.DeepEqual(got, want.Events[i].Chord) {
					t.Fatalf("chord %d %#v got %#v Source %#v", i, event, got, want.Events[i].Chord)
				}
			}
			for i, binding := range bindings {
				if binding.Action == nil {
					continue
				}
				got, ok := BindingDisplay(*binding.Action, binding.Context, bindings)
				if ok != (want.Displays[i] != nil) || ok && got != *want.Displays[i] {
					t.Fatal("binding display differs", i, got, want.Displays[i])
				}
			}
			for i, slash := range slashes {
				if got := ParseSlashCommand(slash); !reflect.DeepEqual(got, want.Slashes[i]) {
					t.Fatalf("slash %q got %#v Source %#v", slash, got, want.Slashes[i])
				}
			}
		})
	}
}
