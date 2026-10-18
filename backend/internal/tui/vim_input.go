package tui

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// VimMode is presentation only. The session continues to own all execution.
func (s *State) VimMode() string {
	if s.Preferences.EditorMode != "vim" {
		return ""
	}
	if s.vim == nil {
		return "INSERT"
	}
	return s.vim.Mode
}

func (s *State) handleVimKey(key Key, now time.Time) (bool, []action) {
	if s.Preferences.EditorMode != "vim" || s.Searching || s.Transcript || s.WorktreeExit != nil || s.ModelPicker != nil || s.LocalPanel != nil || len(s.Permissions) > 0 {
		return false, nil
	}
	if s.vim == nil {
		s.vim = NewVimEditor()
	}
	v := s.vim
	v.sync(&s.Editor)
	// Source's stateful inputFilter disarms on every key, but its transformed
	// input is used only in INSERT. NORMAL commands retain their literal input.
	pending := s.pendingImageSpace
	s.pendingImageSpace = false
	if pending && v.Mode == "INSERT" && styleNonSpacePrintable(key) {
		key.Text = " " + key.Text
	}
	if v.HandleKey(key, s.Width) {
		v.publish(&s.Editor)
		s.Scroll = 0
		s.updateCompletions()
		return true, nil
	}
	// These are the original Cursor base-input editing operations. Keep them
	// in code units even when the current field is representable in UTF-8.
	// Submission, history fallback and configurable controls remain outer I/O.
	if !strings.HasPrefix(key.Name, "ctrl+") && key.Name != "enter" && (key.Text != "" || key.Name == "backspace" || key.Name == "delete" || key.Name == "left" || key.Name == "right" || key.Name == "home" || key.Name == "end") {
		if s.Mode == "prompt" && len(v.Text) == 0 && key.Text == "!" {
			s.Mode = "bash"
			return true, nil
		}
		if v.rawBaseKey(key, s.Width) {
			v.publish(&s.Editor)
			s.historyIndex, s.Scroll = 0, 0
			s.updateCompletions()
			return true, nil
		}
	}
	return false, nil
}

func (s *State) vimSubmissionTextJSON(pastes map[int]PastedContent) json.RawMessage {
	if s.vim == nil {
		return nil
	}
	s.vim.sync(&s.Editor)
	text := vimClone(s.vim.Text)
	view := vimDisplay(text)
	refs := pasteReference.FindAllStringSubmatchIndex(view, -1)
	for i := len(refs) - 1; i >= 0; i-- {
		ref := refs[i]
		id, err := strconv.Atoi(view[ref[4]:ref[5]])
		paste, found := pastes[id]
		if err != nil || id <= 0 || !found || paste.Type != "text" {
			continue
		}
		text = vimSplice(text, vimUnitOffset(s.vim.Text, ref[0]), vimUnitOffset(s.vim.Text, ref[1]), vimUnits(paste.Content))
	}
	return protocol.QuoteJavaScriptUTF16(text)
}
