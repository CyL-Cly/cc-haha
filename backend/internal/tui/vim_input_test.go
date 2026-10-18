package tui

import (
	"testing"
	"time"
)

func TestVimTerminalConsumerPreservesRawSubmissionAndPasteReferenceIdentity(t *testing.T) {
	s := NewState(nil, 80, 25)
	s.SetTerminalPreferences(TerminalPreferences{EditorMode: "vim"})
	s.Editor.Set("😀")
	s.HandleKey(Key{Name: "esc"}, time.Now())
	if s.VimMode() != "NORMAL" || s.vim.Offset != 1 {
		t.Fatal(s.VimMode(), s.vim)
	}
	s.HandleKey(Key{Text: "x"}, time.Now())
	if got := string(s.vimSubmissionTextJSON(nil)); got != `"\ud83d"` {
		t.Fatal(got)
	}
	s.HandleKey(Key{Text: "i"}, time.Now())
	s.HandleKey(Key{Text: " [Pasted text #1] "}, time.Now())
	s.PastedContents = map[int]PastedContent{1: {Type: "text", Content: "literal [Pasted text #2]"}, 2: {Type: "text", Content: "must not expand twice"}}
	got := string(s.vimSubmissionTextJSON(s.PastedContents))
	if got != `" [Pasted text #1] \ud83d"` && got != `" literal [Pasted text #2] \ud83d"` {
		t.Fatal(got)
	}
	if got != `" literal [Pasted text #2] \ud83d"` {
		t.Fatal("original ref must expand once", got)
	}
	actions := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(actions) != 1 || actions[0].kind != actionSubmit || string(actions[0].input.TextJSON) != got {
		t.Fatal(actions)
	}
}

func TestVimTerminalModalPriorityFilterDisarmingAndConfigModeConsumer(t *testing.T) {
	s := NewState(nil, 80, 25)
	s.SetTerminalPreferences(TerminalPreferences{EditorMode: "vim"})
	s.Editor.Set("[Image #2]")
	s.pendingImageSpace = true
	s.HandleKey(Key{Name: "esc"}, time.Now())
	s.HandleKey(Key{Text: "i"}, time.Now())
	s.HandleKey(Key{Text: "Q"}, time.Now())
	if s.Editor.Text() != "[Image #2Q]" {
		t.Fatal("Source filter stays disarmed across NORMAL/INSERT", s.Editor.Text())
	}
	s.Permissions = []PermissionRequest{{ID: "allow", ToolName: "Write", Choices: []PermissionChoice{{Key: "y", Decision: PermissionDecision{Allowed: true}}}}}
	actions := s.HandleKey(Key{Name: "y", Text: "y"}, time.Now())
	if len(actions) != 1 || actions[0].kind != actionPermission || !actions[0].decision.Allowed {
		t.Fatal(actions)
	}
	s.SetTerminalPreferences(TerminalPreferences{EditorMode: "normal"})
	if s.VimMode() != "" {
		t.Fatal(s.VimMode())
	}
	s.Editor.Reset()
	s.HandleKey(Key{Text: "dw"}, time.Now())
	if s.Editor.Text() != "dw" {
		t.Fatal(s.Editor.Text())
	}
}
