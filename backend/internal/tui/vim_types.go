package tui

import (
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/rivo/uniseg"
	"golang.org/x/text/unicode/norm"
)

const vimMaximumCount = 10000

type vimCommand struct {
	Type, Operator, Digits, Find, Scope, Direction string
	Count                                          int
}
type vimRecordedChange struct {
	Type, Operator, Motion, Find, Scope, Object, Direction string
	Text, Character                                        []uint16
	Count                                                  int
}
type vimFind struct {
	Type      string
	Character []uint16
}
type vimSnapshot struct {
	Text   []uint16
	Offset int
}

// VimEditor retains the original code-unit field for editing/submission. Its
// UTF-8 projection belongs only to the existing terminal display; a Source
// command can legitimately leave a lone surrogate in the underlying field.
type VimEditor struct {
	Mode               string
	Command            vimCommand
	InsertedText       []uint16
	Register           []uint16
	RegisterIsLinewise bool
	LastFind           *vimFind
	LastChange         *vimRecordedChange
	Text               []uint16
	Offset             int
	Undo               []vimSnapshot
	Replay             bool
	mirrorText         string
	mirrorOffset       int
	mirrored           bool
}

func NewVimEditor() *VimEditor           { return &VimEditor{Mode: "INSERT", Command: vimCommand{Type: "idle"}} }
func (v *VimEditor) TextJSON() []byte    { return protocol.QuoteJavaScriptUTF16(v.Text) }
func vimUnits(text string) []uint16      { return utf16.Encode([]rune(text)) }
func vimDisplay(text []uint16) string    { return string(utf16.Decode(text)) }
func vimEqual(left, right []uint16) bool { return slices.Equal(left, right) }
func vimClone(text []uint16) []uint16    { return slices.Clone(text) }
func vimSplice(text []uint16, start, end int, inserted []uint16) []uint16 {
	start, end = min(max(start, 0), len(text)), min(max(end, 0), len(text))
	result := append(vimClone(text[:start]), inserted...)
	return append(result, text[end:]...)
}
func vimNFC(text []uint16) []uint16 {
	// Normalize valid runs while retaining lone original code units verbatim.
	// Converting the entire string through utf16.Decode would silently lose
	// the Source field's surrogate identity.
	result, run := []uint16{}, []uint16{}
	flush := func() { result = append(result, vimUnits(norm.NFC.String(vimDisplay(run)))...); run = nil }
	for i := 0; i < len(text); i++ {
		unit := text[i]
		if unit >= 0xd800 && unit <= 0xdbff && i+1 < len(text) && text[i+1] >= 0xdc00 && text[i+1] <= 0xdfff {
			run = append(run, unit, text[i+1])
			i++
			continue
		}
		if unit >= 0xd800 && unit <= 0xdfff {
			flush()
			result = append(result, unit)
			continue
		}
		run = append(run, unit)
	}
	flush()
	return result
}
func vimByteOffset(text []uint16, offset int) int {
	byteOffset := 0
	for i := 0; i < min(offset, len(text)); i++ {
		if text[i] >= 0xd800 && text[i] <= 0xdbff && i+1 < len(text) && text[i+1] >= 0xdc00 && text[i+1] <= 0xdfff {
			if i+1 >= offset {
				return byteOffset
			}
			byteOffset += 4
			i++
		} else {
			byteOffset += len(vimDisplay(text[i : i+1]))
		}
	}
	return byteOffset
}
func vimUnitOffset(text []uint16, bytes int) int {
	for i := 0; i <= len(text); i++ {
		if vimByteOffset(text, i) >= bytes {
			return i
		}
	}
	return len(text)
}

type vimGrapheme struct {
	Start, End, Width int
	Units             []uint16
}

func vimGraphemes(text []uint16) []vimGrapheme {
	// An unpaired unit is rendered as one replacement glyph for measurement,
	// but the slices below still address the original field, never that glyph.
	view := vimDisplay(text)
	result := []vimGrapheme{}
	segmenter := uniseg.NewGraphemes(view)
	for segmenter.Next() {
		start, end := segmenter.Positions()
		from, to := vimUnitOffset(text, start), vimUnitOffset(text, end)
		result = append(result, vimGrapheme{from, to, segmenter.Width(), text[from:to]})
	}
	return result
}
func vimFirst(text []uint16) []uint16 {
	if values := vimGraphemes(text); len(values) > 0 {
		return values[0].Units
	}
	return nil
}
func vimLast(text []uint16) []uint16 {
	if values := vimGraphemes(text); len(values) > 0 {
		return values[len(values)-1].Units
	}
	return nil
}
func (v *VimEditor) record(change vimRecordedChange) {
	if !v.Replay {
		v.LastChange = &change
	}
}
func (v *VimEditor) change(text []uint16) {
	v.Undo = append(v.Undo, vimSnapshot{vimClone(v.Text), v.Offset})
	v.Text = vimClone(text)
}
func (v *VimEditor) enterInsert(offset int) {
	v.Offset = offset
	v.Mode = "INSERT"
	v.InsertedText = nil
	v.Command = vimCommand{Type: "idle"}
}
func (v *VimEditor) escape() {
	if v.Mode == "INSERT" {
		if len(v.InsertedText) > 0 {
			v.record(vimRecordedChange{Type: "insert", Text: vimClone(v.InsertedText)})
		}
		if v.Offset > 0 && v.Offset <= len(v.Text) && v.Text[v.Offset-1] != '\n' {
			v.Offset--
		}
	}
	v.Mode = "NORMAL"
	v.Command = vimCommand{Type: "idle"}
}
func (v *VimEditor) sync(editor *Editor) {
	if editor.Text() != v.mirrorText || editor.Cursor() != v.mirrorOffset {
		if v.mirrored && editor.Text() != v.mirrorText {
			v.Undo = append(v.Undo, vimSnapshot{vimClone(v.Text), v.Offset})
		}
		v.Text = vimUnits(editor.Text())
		v.Offset = vimUnitOffset(v.Text, editor.Cursor())
		// Record the controlled input we just consumed. A second external
		// update can return to an older projection before the next Vim key.
		v.mirrorText, v.mirrorOffset = editor.Text(), editor.Cursor()
	}
}
func (v *VimEditor) publish(editor *Editor) {
	view := vimDisplay(v.Text)
	if view != editor.text {
		editor.snapshot()
		editor.text = view
	}
	editor.cursor = vimByteOffset(v.Text, min(max(v.Offset, 0), len(v.Text)))
	v.mirrorText, v.mirrorOffset = editor.Text(), editor.Cursor()
	v.mirrored = true
}

func (v *VimEditor) rawEditingNeeded() bool {
	for i := 0; i < len(v.Text); i++ {
		unit := v.Text[i]
		if unit >= 0xd800 && unit <= 0xdbff && i+1 < len(v.Text) && v.Text[i+1] >= 0xdc00 && v.Text[i+1] <= 0xdfff {
			if v.Offset == i+1 {
				return true
			}
			i++
			continue
		}
		if unit >= 0xd800 && unit <= 0xdfff {
			return true
		}
	}
	return false
}
func (v *VimEditor) rawBaseKey(key Key, columns int) bool {
	cursor := v.cursor(columns)
	switch key.Name {
	case "left", "ctrl+b":
		v.Offset = cursor.left().offset
	case "right", "ctrl+f":
		v.Offset = cursor.right().offset
	case "up":
		v.Offset = cursor.visual(-1).offset
	case "down":
		v.Offset = cursor.visual(1).offset
	case "ctrl+a", "home":
		v.Offset = cursor.lineStart(cursor.offset)
	case "ctrl+e", "end":
		v.Offset = cursor.lineEnd(cursor.offset)
	case "backspace", "ctrl+h":
		if cursor.offset > 0 {
			from := cursor.left().offset
			v.change(vimNFC(vimSplice(cursor.text, from, cursor.offset, nil)))
			v.Offset = from
		}
	case "delete", "ctrl+d":
		if cursor.offset < len(cursor.text) {
			v.change(vimNFC(vimSplice(cursor.text, cursor.offset, cursor.right().offset, nil)))
			v.Offset = cursor.offset
		}
	default:
		if key.Text == "" || strings.HasPrefix(key.Name, "ctrl+") {
			return false
		}
		units := vimUnits(key.Text)
		v.change(vimNFC(vimSplice(cursor.text, cursor.offset, cursor.offset, units)))
		v.Offset = cursor.offset + len(vimNFC(units))
	}
	return true
}
