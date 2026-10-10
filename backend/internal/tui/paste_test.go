package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLongPasteUsesOriginalUTF16ThresholdAndOneTimeExpansion(t *testing.T) {
	for _, test := range []struct {
		text      string
		collapsed bool
	}{{strings.Repeat("😀", 400), false}, {strings.Repeat("😀", 401), true}, {"one\ntwo\nthree", false}, {"one\ntwo\nthree\nfour", true}} {
		s := NewState(nil, 80, 24)
		s.Paste(test.text)
		display := s.Editor.Text()
		if strings.HasPrefix(display, "[Pasted text #") != test.collapsed {
			t.Fatalf("wrong collapse for %d bytes: %q", len(test.text), display)
		}
		actions := key(s, "enter")
		if len(actions) != 1 || actions[0].input.Text != test.text || actions[0].input.PreExpansionText != display {
			t.Fatalf("submission did not retain original paste: %+v", actions)
		}
	}
	pastes := map[int]PastedContent{1: {ID: 1, Type: "text", Content: "literal [Pasted text #2]"}, 2: {ID: 2, Type: "text", Content: "second"}}
	if got := ExpandPastedTextRefs("[Pasted text #1] [Pasted text #2] [Image #2]", pastes); got != "literal [Pasted text #2] second second" {
		t.Fatal("nested paste references were expanded more than once", got)
	}
}

func TestImagePillsLazySpaceMetadataAndDeletionBoundary(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Mode = "bash"
	dimensions := json.RawMessage(`{"width":80,"height":40,"originalWidth":160}`)
	s.PasteImage(PastedContent{Content: "base64-one", Dimensions: dimensions, SourcePath: "/original/image.png"})
	s.PasteImage(PastedContent{Content: "base64-two", MediaType: "image/jpeg", Filename: "original.jpg"})
	dimensions[0] = 'x'
	if s.Mode != "prompt" || s.Editor.Text() != "[Image #1] [Image #2]" || string(s.PastedContents[1].Dimensions) != `{"width":80,"height":40,"originalWidth":160}` {
		t.Fatal("image metadata/pill mode changed", s)
	}
	s.HandleKey(Key{Name: "x", Text: "x"}, time.Now())
	if s.Editor.Text() != "[Image #1] [Image #2] x" {
		t.Fatal("missing lazy pill space", s.Editor.Text())
	}
	s.Editor.Set("[Image #2] explanation")
	in := key(s, "enter")[0].input
	if len(in.PastedContents) != 1 || in.PastedContents[2].Content != "base64-two" || in.Text != "[Image #2] explanation" {
		t.Fatal("deleted image was submitted", in)
	}
	if in.PastedContents[2].MediaType != "image/jpeg" || in.PastedContents[2].Filename != "original.jpg" {
		t.Fatal("original image metadata changed", in)
	}
}

func TestPasteContentsSurviveUndoHistoryStashEditorAndCancellation(t *testing.T) {
	s := NewState(nil, 80, 24)
	text := strings.Repeat("中文😀", 300)
	s.Paste(text)
	display := s.Editor.Text()
	key(s, "ctrl+s")
	if len(s.PastedContents) != 0 || s.Editor.Text() != "" {
		t.Fatal("stash retained active paste")
	}
	key(s, "ctrl+s")
	if s.Editor.Text() != display || ExpandPastedTextRefs(s.Editor.Text(), s.PastedContents) != text {
		t.Fatal("stash lost raw paste")
	}
	key(s, "ctrl+u")
	key(s, "ctrl+_")
	if s.Editor.Text() != display {
		t.Fatal("undo lost paste reference", s.Editor.Text())
	}
	acts := key(s, "ctrl+g")
	if len(acts) != 1 || acts[0].text != text {
		t.Fatal("external editor received a pill instead of original paste", acts)
	}
	m := terminalModel{state: s}
	m.Update(editorReturnedMsg{text: text + " changed", changed: true})
	if s.Editor.Text() != display+" changed" {
		t.Fatal("unchanged paste did not re-collapse after editor", s.Editor.Text())
	}
	first := key(s, "enter")[0].input
	key(s, "up")
	if s.Editor.Text() != display+" changed" || ExpandPastedTextRefs(s.Editor.Text(), s.PastedContents) != text+" changed" {
		t.Fatal("history lost paste metadata")
	}
	key(s, "enter")
	s.Editor.Set("draft")
	key(s, "esc")
	s.FinishTurn(first.TurnID, nil)
	if s.Editor.Text() != text+" changed\ndraft" || s.Mode != "prompt" {
		t.Fatal("cancelled queue must restore expanded Source text", s.Editor.Text())
	}
}

func TestPillLazySpaceIsConsumedByMovementOrPaste(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.PasteImage(PastedContent{Content: "image"})
	key(s, "left")
	s.HandleKey(Key{Name: "x", Text: "x"}, time.Now())
	if s.Editor.Text() != "[Image #1x]" {
		t.Fatal("movement did not disarm lazy space", s.Editor.Text())
	}
	s.Editor.Reset()
	s.PasteImage(PastedContent{Content: "image"})
	s.Paste("text")
	if s.Editor.Text() != "[Image #2]text" {
		t.Fatal("paste must disarm lazy space", s.Editor.Text())
	}
}
