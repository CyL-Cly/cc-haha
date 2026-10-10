package tui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEditingNeverSplitsChineseOrExtendedEmoji(t *testing.T) {
	for _, cluster := range []string{"中", "👨‍👩‍👧‍👦", "👍🏽", "🇨🇳", "e\u0301"} {
		t.Run(cluster, func(t *testing.T) {
			var e Editor
			e.Insert("a" + cluster + "z")
			e.Left()
			e.Backspace()
			if e.Text() != "az" || e.Cursor() != 1 {
				t.Fatalf("split grapheme: %q @%d", e.Text(), e.Cursor())
			}
			e.Undo()
			if e.Text() != "a"+cluster+"z" {
				t.Fatalf("undo lost input: %q", e.Text())
			}
			e.Left()
			e.Delete()
			if e.Text() != "az" {
				t.Fatalf("forward delete split grapheme: %q", e.Text())
			}
		})
	}
}

func TestCombiningInputAcrossKeyEventsUsesWholeGrapheme(t *testing.T) {
	var e Editor
	e.Insert("e")
	e.Insert("\u0301")
	e.Left()
	if e.Cursor() != 0 {
		t.Fatalf("combining sequence split at %d", e.Cursor())
	}
	e.Delete()
	if e.Text() != "" {
		t.Fatalf("combining sequence survived delete: %q", e.Text())
	}
}

func TestPasteRetainsAllContentWithoutExecutingEmbeddedEnter(t *testing.T) {
	var e Editor
	raw := "\x1b[31m中文\x1b[0m\r\n\t👨‍👩‍👧‍👦\n" + strings.Repeat("x", 12000)
	e.Paste(raw)
	want := "中文\n\n    👨‍👩‍👧‍👦\n" + strings.Repeat("x", 12000)
	if e.Text() != want {
		t.Fatalf("paste data changed: bytes %d want %d", len(e.Text()), len(want))
	}
	if !utf8.ValidString(e.Text()) {
		t.Fatal("paste produced invalid UTF-8")
	}
	e.Undo()
	if e.Text() != "" {
		t.Fatal("paste did not have a single undo boundary")
	}
}

func TestVerticalNavigationMeasuresDisplayCells(t *testing.T) {
	var e Editor
	e.Set("中国x\nabcde\n👍🏽z")
	e.Home()
	e.Right()
	if !e.Vertical(-1) || e.Cursor() != len("中国x\nab") {
		t.Fatalf("emoji column did not map to two cells: @%d", e.Cursor())
	}
	if !e.Vertical(-1) || e.Cursor() != len("中") {
		t.Fatalf("CJK column did not map to grapheme: @%d", e.Cursor())
	}
	if e.Vertical(-1) {
		t.Fatal("first logical line consumed history navigation")
	}
}

func TestKillYankAndUndoPreserveMultilineBytes(t *testing.T) {
	var e Editor
	e.Set("first\n中文 👋 last")
	e.KillWordBefore()
	if e.Text() != "first\n中文 👋 " {
		t.Fatalf("kill: %q", e.Text())
	}
	e.Yank()
	if e.Text() != "first\n中文 👋 last" {
		t.Fatalf("yank: %q", e.Text())
	}
	e.Home()
	e.KillToEnd()
	if e.Text() != "first\n" {
		t.Fatalf("kill line: %q", e.Text())
	}
	e.Undo()
	if e.Text() != "first\n中文 👋 last" {
		t.Fatal("undo did not restore full line")
	}
}

func TestConsecutiveKillAndYankPopFollowInputSequence(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("one two three")
	key(s, "ctrl+w")
	key(s, "ctrl+w")
	key(s, "ctrl+y")
	if s.Editor.Text() != "one two three" {
		t.Fatalf("consecutive backwards kills lost order: %q", s.Editor.Text())
	}
	key(s, "left")
	key(s, "right")
	key(s, "ctrl+w")
	key(s, "ctrl+y")
	key(s, "alt+y")
	if s.Editor.Text() != "one two two three" {
		t.Fatalf("yank-pop did not replace previous yank: %q", s.Editor.Text())
	}
	before := s.Editor.Text()
	key(s, "left")
	key(s, "alt+y")
	if s.Editor.Text() != before {
		t.Fatal("movement failed to end yank-pop sequence")
	}
}
