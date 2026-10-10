package tui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// PastedContent is the original input attachment metadata. Image validation,
// resize, storage and conversion to model blocks belong to the shared host.
type PastedContent struct {
	ID         int             `json:"id"`
	Type       string          `json:"type"`
	Content    string          `json:"content"`
	MediaType  string          `json:"mediaType,omitempty"`
	Filename   string          `json:"filename,omitempty"`
	Dimensions json.RawMessage `json:"dimensions,omitempty"`
	SourcePath string          `json:"sourcePath,omitempty"`
}

type HistoryEntry struct {
	Display        string
	PastedContents map[int]PastedContent
}

var pasteReference = regexp.MustCompile(`\[(Pasted text|Image|\.\.\.Truncated text) #(\d+)(?: \+\d+ lines)?(\.)*\]`)

func pastedTextRef(id int, content string) string {
	lines := strings.Count(content, "\n")
	if lines == 0 {
		return fmt.Sprintf("[Pasted text #%d]", id)
	}
	return fmt.Sprintf("[Pasted text #%d +%d lines]", id, lines)
}

func clonePastes(values map[int]PastedContent) map[int]PastedContent {
	if len(values) == 0 {
		return nil
	}
	result := make(map[int]PastedContent, len(values))
	for id, value := range values {
		value.Dimensions = append(json.RawMessage(nil), value.Dimensions...)
		result[id] = value
	}
	return result
}

// FilterReferencedPastes follows the Source image-pill deletion boundary.
// Text entries remain available to history/editor expansion even after edits.
func FilterReferencedPastes(text string, values map[int]PastedContent) map[int]PastedContent {
	referenced := map[int]bool{}
	for _, ref := range pasteReference.FindAllStringSubmatch(text, -1) {
		if id, err := strconv.Atoi(ref[2]); err == nil && id > 0 {
			referenced[id] = true
		}
	}
	result := clonePastes(values)
	for id, value := range result {
		if value.Type == "image" && !referenced[id] {
			delete(result, id)
		}
	}
	return result
}

// ExpandPastedTextRefs splices original reference offsets in reverse order.
// Placeholder-like text inside a paste is literal and is never expanded twice.
func ExpandPastedTextRefs(text string, values map[int]PastedContent) string {
	refs := pasteReference.FindAllStringSubmatchIndex(text, -1)
	for i := len(refs) - 1; i >= 0; i-- {
		ref := refs[i]
		id, err := strconv.Atoi(text[ref[4]:ref[5]])
		value, found := values[id]
		if err != nil || id <= 0 || !found || value.Type != "text" {
			continue
		}
		text = text[:ref[0]] + value.Content + text[ref[1]:]
	}
	return text
}

func recollapsePastedText(text string, values map[int]PastedContent) string {
	ids := make([]int, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		value := values[id]
		if value.Type != "text" {
			continue
		}
		if at := strings.Index(text, value.Content); at >= 0 {
			text = text[:at] + pastedTextRef(id, value.Content) + text[at+len(value.Content):]
		}
	}
	return text
}

func (s *State) addPaste(value PastedContent) int {
	s.pasteSequence++
	value.ID = s.pasteSequence
	if s.PastedContents == nil {
		s.PastedContents = map[int]PastedContent{}
	}
	value.Dimensions = append(json.RawMessage(nil), value.Dimensions...)
	s.PastedContents[value.ID] = value
	return value.ID
}

// PasteImage only retains host-produced original image data and inserts its
// inline reference. It never reads a clipboard, validates or resizes an image.
func (s *State) PasteImage(value PastedContent) {
	if s.Closing || s.Editing || s.Transcript || s.Searching || len(s.Permissions) != 0 {
		return
	}
	s.Mode = "prompt"
	value.Type = "image"
	if value.MediaType == "" {
		value.MediaType = "image/png"
	}
	if value.Filename == "" {
		value.Filename = "Pasted image"
	}
	id := s.addPaste(value)
	if s.pendingImageSpace {
		s.Editor.Insert(" ")
	}
	s.Editor.Insert(fmt.Sprintf("[Image #%d]", id))
	s.pendingImageSpace = true
	s.historyIndex = 0
	s.updateCompletions()
}

func collapsedPaste(text string, height int) bool {
	return len(utf16.Encode([]rune(text))) > 800 || strings.Count(text, "\n") > min(height-10, 2)
}

func styleNonSpacePrintable(key Key) bool {
	if key.Text == "" || strings.HasPrefix(key.Text, "\x1b") || strings.Contains(key.Name, "+") {
		return false
	}
	first := []rune(key.Text)[0]
	return strings.TrimSpace(string(first)) != ""
}
