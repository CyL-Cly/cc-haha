package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeTerminalPreferenceConsumersAndWarningLifetime(t *testing.T) {
	s := NewState(nil, 80, 30)
	s.Editor.Set("private draft")
	s.SetTerminalPreferences(TerminalPreferences{Theme: "light", Verbose: true, ReducedMotion: true, ProgressBarEnabled: true, ShowTurnDuration: true, ShowTips: true, SpinnerTip: "Source actual tip"})
	now := time.Now()
	s.Tick(now)
	s.start(Input{TurnID: "turn", Text: "actual human"})
	view := s.View("title", true)
	if view.ProgressBar == nil || !strings.Contains(view.Content, "\x1b[38;2;215;119;87m") || strings.Contains(view.Content, "✽ Working") {
		t.Fatal(view)
	}
	if !strings.Contains(view.Content, "Source actual tip") {
		t.Fatal("enabled tip consumer missing")
	}
	preferences := s.Preferences
	preferences.ShowTips = false
	s.Apply(Event{Kind: TerminalPreferencesChanged, Preferences: &preferences})
	if !strings.Contains(s.View("title", true).Content, "Source actual tip") {
		t.Fatal("settings change discarded the Source retained spinnerTip")
	}
	preferences.SpinnerTip = ""
	s.Apply(Event{Kind: TerminalPreferencesChanged, Preferences: &preferences})
	if strings.Contains(s.View("title", true).Content, "Source actual tip") {
		t.Fatal("cleared Source spinnerTip rendered")
	}
	s.SetKeybindingWarning("1 invalid keybinding configuration · /doctor for details", now)
	if !strings.Contains(s.View("", true).Content, "/doctor") {
		t.Fatal("cached warnings not rendered")
	}
	s.Notification = "ordinary notice retained"
	s.SetKeybindingWarning("", now)
	if strings.Contains(s.View("", true).Content, "/doctor") || s.Notification != "ordinary notice retained" {
		t.Fatal("fixed configuration did not clear only its own warning")
	}
	s.SetKeybindingWarning("1 invalid keybinding configuration · /doctor for details", now)
	s.Tick(now.Add(61 * time.Second))
	if strings.Contains(s.View("", true).Content, "/doctor") {
		t.Fatal("warning did not expire")
	}
	s.FinishTurn("turn", nil)
	if !strings.Contains(strings.Join(s.transcriptLines(), "\n"), "Worked for") {
		t.Fatal("duration consumer")
	}
	s.SetTerminalPreferences(TerminalPreferences{Theme: "auto"})
	s.LightBackground = true
	if s.themeAccent() != "\x1b[38;2;215;119;87m" || strings.Contains(strings.Join(s.transcriptLines(), "\n"), "Worked for") {
		t.Fatal("auto palette or duration hide")
	}
	for _, theme := range []string{"dark", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		s.Preferences.Theme = theme
		if s.themeAccent() == "" {
			t.Fatal(theme)
		}
	}
	if s.Editor.Text() != "private draft" {
		t.Fatal("display preferences changed human input")
	}
}

func TestActualMountedSourceSpinnerDisabledTipSettingRetainsThenClearsSelectedTip(t *testing.T) {
	const message = "Preference source tip"
	const retained = "Source actual tip"
	const renewed = "Source renewed tip"
	// This existing whole mounted fixture sets actual spinnerTipsEnabled=false.
	// Spinner's selected-tip prop survives that setting until the root producer
	// clears it; the setting separately gates time-based tip overrides.
	row := map[string]any{"tasks": []any{}, "theme": "dark", "width": 80, "height": 36, "loading": true, "mode": "responding", "message": message, "spinnerTip": retained, "responseLength": 0, "elapsedMs": 500, "pauseElapsedMs": 500, "global": map[string]any{"btwUseCount": 1}, "steps": []map[string]any{{"props": map[string]any{"spinnerTip": nil}}, {"props": map[string]any{"spinnerTip": renewed}}}}
	frames := terminalSpinnerOriginalFrames(t, true, []map[string]any{row})[0]
	if len(frames) != 3 {
		t.Fatal("original selected-tip transitions missing", len(frames))
	}
	s := NewState(nil, 80, 36)
	s.ActiveTurn = "actual-preference-query"
	clock := s.ensureTerminalSpinnerClock()
	clock.mode, clock.verb, clock.verbKnown, clock.loading = "responding", message, true, true
	clock.overrideMessage = new(string)
	*clock.overrideMessage = message
	base := time.UnixMilli(frames[0].Now)
	clock.started, clock.pausedAt = base.Add(-500*time.Millisecond), base
	s.dreamClock = base
	for index, selected := range []string{retained, "", renewed} {
		preferences := TerminalPreferences{Theme: "dark", ReducedMotion: true, ShowTips: false, SpinnerTip: selected}
		s.Apply(Event{Kind: TerminalPreferencesChanged, Preferences: &preferences})
		source := strings.Join(frames[index].Rows, "\n")
		if selected != "" && !strings.Contains(source, "Tip: "+selected) || selected == "" && (strings.Contains(source, retained) || strings.Contains(source, renewed)) {
			t.Fatalf("actual Source disabled-setting selected-tip step%d: %q", index, frames[index].Rows)
		}
		got := terminalSpinnerPublicRegion(s, len(frames[index].PaintRows), message)
		if !reflect.DeepEqual(terminalTaskListCells(frames[index].PaintRows, s.Width), terminalTaskListCells(got, s.Width)) {
			t.Errorf("disabled-setting selected-tip step%d Source%q Go%q", index, frames[index].PaintRows, got)
		}
	}
}
