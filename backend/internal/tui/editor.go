package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

type editSnapshot struct {
	text   string
	cursor int
}

// Editor keeps a byte cursor on an extended grapheme boundary. Combining
// marks, surrogate-free UTF-8 and joined emoji remain intact while editing.
type Editor struct {
	text        string
	cursor      int
	undo        []editSnapshot
	killed      []string
	lastWasKill bool
	lastWasYank bool
	yankStart   int
	yankLength  int
	yankIndex   int
}

func (e *Editor) Text() string { return e.text }
func (e *Editor) Cursor() int  { return e.cursor }

func (e *Editor) Set(text string) {
	e.text = strings.ToValidUTF8(text, "�")
	e.cursor = len(e.text)
}

func (e *Editor) Reset() {
	e.text, e.cursor = "", 0
	e.undo = nil
	e.lastWasKill, e.lastWasYank = false, false
}

func (e *Editor) snapshot() {
	e.undo = append(e.undo, editSnapshot{e.text, e.cursor})
	if len(e.undo) > 100 {
		e.undo = e.undo[len(e.undo)-100:]
	}
}

func (e *Editor) Undo() {
	if len(e.undo) == 0 {
		return
	}
	s := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.text, e.cursor = s.text, s.cursor
	e.lastWasKill, e.lastWasYank = false, false
}

func (e *Editor) Insert(text string) {
	text = strings.ToValidUTF8(text, "�")
	if text == "" {
		return
	}
	e.snapshot()
	e.lastWasKill, e.lastWasYank = false, false
	e.text = e.text[:e.cursor] + text + e.text[e.cursor:]
	e.cursor += len(text)
	// Inserting a combining mark can join the following cluster. Reposition
	// at that cluster's end instead of leaving the cursor inside a grapheme.
	e.cursor = boundaryAtOrAfter(e.text, e.cursor)
}

// Paste follows PromptInput.onTextPaste: remove terminal escapes, replace CR
// with LF and expand tabs to four spaces. Paste never means "submit".
func normalizedPaste(text string) string {
	text = ansi.Strip(text)
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.ReplaceAll(text, "\t", "    ")
}
func (e *Editor) Paste(text string) { e.Insert(normalizedPaste(text)) }

func (e *Editor) prepareKey(kill, yank bool) {
	if !kill {
		e.lastWasKill = false
	}
	if !yank {
		e.lastWasYank = false
	}
}

func boundaries(text string) []int {
	result := []int{0}
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		_, end := g.Positions()
		result = append(result, end)
	}
	return result
}

func boundaryAtOrAfter(text string, offset int) int {
	for _, at := range boundaries(text) {
		if at >= offset {
			return at
		}
	}
	return len(text)
}

func (e *Editor) Left() {
	previous := 0
	for _, at := range boundaries(e.text) {
		if at >= e.cursor {
			break
		}
		previous = at
	}
	e.cursor = previous
}

func (e *Editor) Right() {
	for _, at := range boundaries(e.text) {
		if at > e.cursor {
			e.cursor = at
			return
		}
	}
}

func (e *Editor) delete(start, end int, kill bool) {
	if start >= end {
		return
	}
	e.snapshot()
	if kill {
		value := e.text[start:end]
		if e.lastWasKill && len(e.killed) > 0 {
			if end <= e.cursor {
				e.killed[len(e.killed)-1] = value + e.killed[len(e.killed)-1]
			} else {
				e.killed[len(e.killed)-1] += value
			}
		} else {
			e.killed = append(e.killed, value)
		}
		if len(e.killed) > 10 {
			e.killed = e.killed[len(e.killed)-10:]
		}
		e.lastWasKill, e.lastWasYank = true, false
	}
	e.text = e.text[:start] + e.text[end:]
	e.cursor = boundaryAtOrAfter(e.text, start)
}

func (e *Editor) Backspace() {
	end := e.cursor
	e.Left()
	start := e.cursor
	e.cursor = end
	e.delete(start, end, false)
}

func (e *Editor) Delete() {
	start := e.cursor
	e.Right()
	end := e.cursor
	e.cursor = start
	e.delete(start, end, false)
}

func (e *Editor) Home() {
	e.cursor = strings.LastIndex(e.text[:e.cursor], "\n") + 1
}

func (e *Editor) End() {
	if index := strings.IndexByte(e.text[e.cursor:], '\n'); index >= 0 {
		e.cursor += index
	} else {
		e.cursor = len(e.text)
	}
}

func (e *Editor) KillToStart() {
	end := e.cursor
	e.Home()
	start := e.cursor
	e.cursor = end
	e.delete(start, end, true)
}

func (e *Editor) KillToEnd() {
	start := e.cursor
	e.End()
	end := e.cursor
	if end == start && end < len(e.text) {
		end++ // A newline has one UTF-8 byte and its own grapheme.
	}
	e.cursor = start
	e.delete(start, end, true)
}

func spaceAt(text string, start, end int) bool {
	r, _ := utf8.DecodeRuneInString(text[start:end])
	return unicode.IsSpace(r)
}

func (e *Editor) WordLeft() {
	b := boundaries(e.text)
	index := 0
	for index < len(b) && b[index] < e.cursor {
		index++
	}
	for index > 0 && spaceAt(e.text, b[index-1], b[index]) {
		index--
	}
	for index > 0 && !spaceAt(e.text, b[index-1], b[index]) {
		index--
	}
	e.cursor = b[index]
}

func (e *Editor) WordRight() {
	b := boundaries(e.text)
	index := 0
	for index < len(b)-1 && b[index] < e.cursor {
		index++
	}
	for index < len(b)-1 && !spaceAt(e.text, b[index], b[index+1]) {
		index++
	}
	for index < len(b)-1 && spaceAt(e.text, b[index], b[index+1]) {
		index++
	}
	e.cursor = b[index]
}

func (e *Editor) KillWordBefore() {
	end := e.cursor
	e.WordLeft()
	start := e.cursor
	e.cursor = end
	e.delete(start, end, true)
}

func (e *Editor) KillWordAfter() {
	start := e.cursor
	e.WordRight()
	end := e.cursor
	e.cursor = start
	e.delete(start, end, false)
}

func (e *Editor) Yank() {
	if len(e.killed) > 0 {
		start := e.cursor
		e.Insert(e.killed[len(e.killed)-1])
		e.yankStart, e.yankLength, e.yankIndex = start, len(e.killed[len(e.killed)-1]), 0
		e.lastWasYank = true
	}
}

func (e *Editor) YankPop() {
	if !e.lastWasYank || len(e.killed) < 2 {
		return
	}
	e.yankIndex = (e.yankIndex + 1) % len(e.killed)
	value := e.killed[len(e.killed)-1-e.yankIndex]
	e.snapshot()
	e.text = e.text[:e.yankStart] + value + e.text[e.yankStart+e.yankLength:]
	e.yankLength = len(value)
	e.cursor = boundaryAtOrAfter(e.text, e.yankStart+e.yankLength)
}

// Vertical moves across logical lines using terminal cells, not UTF-8 bytes.
// False means the outer history navigator should handle the key instead.
func (e *Editor) Vertical(direction int) bool {
	start := strings.LastIndex(e.text[:e.cursor], "\n") + 1
	column := uniseg.StringWidth(e.text[start:e.cursor])
	var targetStart, targetEnd int
	if direction < 0 {
		if start == 0 {
			return false
		}
		targetEnd = start - 1
		targetStart = strings.LastIndex(e.text[:targetEnd], "\n") + 1
	} else {
		newline := strings.IndexByte(e.text[e.cursor:], '\n')
		if newline < 0 {
			return false
		}
		targetStart = e.cursor + newline + 1
		targetEnd = len(e.text)
		if next := strings.IndexByte(e.text[targetStart:], '\n'); next >= 0 {
			targetEnd = targetStart + next
		}
	}
	e.cursor = targetStart
	width := 0
	g := uniseg.NewGraphemes(e.text[targetStart:targetEnd])
	for g.Next() {
		if width+g.Width() > column {
			break
		}
		width += g.Width()
		_, end := g.Positions()
		e.cursor = targetStart + end
	}
	return true
}
