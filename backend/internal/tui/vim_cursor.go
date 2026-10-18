package tui

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
)

var vimImageReference = regexp.MustCompile(`\[Image #[0-9]+\]`)

type vimCursor struct {
	text            []uint16
	offset, columns int
}

func newVimCursor(text []uint16, columns, offset int) vimCursor {
	text = vimNFC(text)
	return vimCursor{text, min(max(offset, 0), len(text)), max(1, columns-1)}
}
func (c vimCursor) at(offset int) vimCursor { c.offset = min(max(offset, 0), len(c.text)); return c }
func (c vimCursor) next(offset int) int {
	for _, g := range vimGraphemes(c.text) {
		if g.End > offset {
			return g.End
		}
	}
	return len(c.text)
}
func (c vimCursor) previous(offset int) int {
	result := 0
	for _, g := range vimGraphemes(c.text) {
		if g.Start >= offset {
			break
		}
		result = g.Start
	}
	return result
}
func (c vimCursor) snap(offset int) int {
	if offset <= 0 {
		return 0
	}
	if offset >= len(c.text) {
		return len(c.text)
	}
	for _, g := range vimGraphemes(c.text) {
		if g.Start <= offset && offset < g.End {
			return g.Start
		}
	}
	return offset
}
func (c vimCursor) grapheme(offset int) []uint16 {
	for _, g := range vimGraphemes(c.text) {
		if g.Start == offset {
			return g.Units
		}
	}
	return nil
}
func (c vimCursor) imageBounds(offset int, ending bool) (int, int, bool) {
	for _, match := range vimImageReference.FindAllStringIndex(vimDisplay(c.text), -1) {
		start, end := vimUnitOffset(c.text, match[0]), vimUnitOffset(c.text, match[1])
		if ending && end == offset || !ending && start == offset {
			return start, end, true
		}
	}
	return 0, 0, false
}
func (c vimCursor) imageSnap(offset int, end bool) int {
	for _, match := range vimImageReference.FindAllStringIndex(vimDisplay(c.text), -1) {
		start, stop := vimUnitOffset(c.text, match[0]), vimUnitOffset(c.text, match[1])
		if offset > start && offset < stop {
			if end {
				return stop
			}
			return start
		}
	}
	return offset
}
func (c vimCursor) left() vimCursor {
	if start, _, ok := c.imageBounds(c.offset, true); ok {
		return c.at(start)
	}
	return c.at(c.previous(c.offset))
}
func (c vimCursor) right() vimCursor {
	if _, end, ok := c.imageBounds(c.offset, false); ok {
		return c.at(end)
	}
	return c.at(c.next(c.offset))
}
func (c vimCursor) lineStart(offset int) int {
	for i := min(offset-1, len(c.text)-1); i >= 0; i-- {
		if c.text[i] == '\n' {
			return i + 1
		}
	}
	return 0
}
func (c vimCursor) lineEnd(offset int) int {
	for i := max(0, offset); i < len(c.text); i++ {
		if c.text[i] == '\n' {
			return i
		}
	}
	return len(c.text)
}
func (c vimCursor) firstNonBlank() vimCursor {
	start, end := c.lineStart(c.offset), c.lineEnd(c.offset)
	for i := start; i < end; i++ {
		if !vimWhitespace(c.text[i : i+1]) {
			return c.at(i)
		}
	}
	return c.at(start)
}
func (c vimCursor) logical(direction int) vimCursor {
	start, end := c.lineStart(c.offset), c.lineEnd(c.offset)
	column := c.offset - start
	if direction < 0 {
		if start == 0 {
			return c.at(0)
		}
		end = start - 1
		start = c.lineStart(end)
	} else {
		if end >= len(c.text) {
			return c.at(len(c.text))
		}
		start = end + 1
		end = c.lineEnd(start)
	}
	return c.at(c.snap(start + min(column, end-start)))
}
func (c vimCursor) goLine(line int) vimCursor {
	offset := 0
	for i := 1; i < line; i++ {
		end := c.lineEnd(offset)
		if end >= len(c.text) {
			return c.at(c.lineStart(len(c.text)))
		}
		offset = end + 1
	}
	return c.at(offset)
}
func (c vimCursor) lastLine() vimCursor {
	for _, unit := range c.text {
		if unit == '\n' {
			return c.at(c.lineStart(len(c.text)))
		}
	}
	line, col := c.position()
	if col == 0 && line > 0 {
		line--
	}
	return c.visualPosition(line, 0)
}
func (c vimCursor) visualPosition(line, column int) vimCursor {
	lines := c.wrapped()
	line = min(max(line, 0), len(lines)-1)
	current := lines[line]
	if len(current.Text) == 0 && current.Newline {
		return c.at(current.Start)
	}
	leading := 0
	if !current.Preceded {
		leading = len(current.Text) - len(vimTrimStart(current.Text))
	}
	target := column + leading
	offset, cells := 0, 0
	for _, g := range vimGraphemes(current.Text) {
		if target <= 0 {
			break
		}
		if cells+g.Width > target {
			break
		}
		cells += g.Width
		offset = g.End
	}
	return c.at(current.Start + min(offset, len(current.Text)))
}
func vimWhitespace(value []uint16) bool {
	for _, r := range utf16.Decode(value) {
		if protocol.IsJavaScriptWhitespace(r) {
			return true
		}
	}
	return false
}
func vimWord(value []uint16) bool {
	decoded := utf16.Decode(value)
	return len(decoded) == 1 && (decoded[0] == '_' || unicode.IsLetter(decoded[0]) || unicode.IsNumber(decoded[0]) || unicode.IsMark(decoded[0]))
}
func vimPunctuation(value []uint16) bool {
	return len(value) > 0 && !vimWhitespace(value) && !vimWord(value)
}
func (c vimCursor) wordMotion(key string) vimCursor {
	pos := c.offset
	if key == "w" {
		current := c.grapheme(pos)
		if len(current) == 0 {
			return c
		}
		if vimWord(current) {
			for pos < len(c.text) && vimWord(c.grapheme(pos)) {
				pos = c.next(pos)
			}
		} else if vimPunctuation(current) {
			for pos < len(c.text) && vimPunctuation(c.grapheme(pos)) {
				pos = c.next(pos)
			}
		}
		for pos < len(c.text) && vimWhitespace(c.grapheme(pos)) {
			pos = c.next(pos)
		}
		return c.at(pos)
	}
	if key == "e" {
		if pos >= len(c.text) || len(c.grapheme(pos)) == 0 {
			return c
		}
		pos = c.next(pos)
		for pos < len(c.text) && vimWhitespace(c.grapheme(pos)) {
			pos = c.next(pos)
		}
		if pos >= len(c.text) {
			return c.at(len(c.text))
		}
		word, punctuation := vimWord(c.grapheme(pos)), vimPunctuation(c.grapheme(pos))
		for pos < len(c.text) {
			next := c.next(pos)
			if next >= len(c.text) || word && !vimWord(c.grapheme(next)) || punctuation && !vimPunctuation(c.grapheme(next)) {
				break
			}
			if !word && !punctuation {
				break
			}
			pos = next
		}
		return c.at(pos)
	}
	if key == "b" {
		if pos == 0 {
			return c
		}
		pos = c.previous(pos)
		for pos > 0 && vimWhitespace(c.grapheme(pos)) {
			pos = c.previous(pos)
		}
		word, punctuation := vimWord(c.grapheme(pos)), vimPunctuation(c.grapheme(pos))
		for pos > 0 {
			previous := c.previous(pos)
			if word && !vimWord(c.grapheme(previous)) || punctuation && !vimPunctuation(c.grapheme(previous)) || !word && !punctuation {
				break
			}
			pos = previous
		}
		return c.at(pos)
	}
	if key == "W" {
		for pos < len(c.text) && !vimWhitespace(c.grapheme(pos)) {
			next := c.at(pos).right().offset
			if next == pos {
				break
			}
			pos = next
		}
		for pos < len(c.text) && vimWhitespace(c.grapheme(pos)) {
			pos = c.at(pos).right().offset
		}
		return c.at(pos)
	}
	if key == "B" {
		cursor := c
		if vimWhitespace(cursor.left().grapheme(cursor.left().offset)) {
			cursor = cursor.left()
		}
		for cursor.offset > 0 && vimWhitespace(cursor.grapheme(cursor.offset)) {
			cursor = cursor.left()
		}
		if !vimWhitespace(cursor.grapheme(cursor.offset)) {
			for cursor.offset > 0 && !vimWhitespace(cursor.left().grapheme(cursor.left().offset)) {
				cursor = cursor.left()
			}
		}
		return cursor
	}
	if key == "E" {
		if pos >= len(c.text) {
			return c
		}
		cursor := c
		if !vimWhitespace(cursor.grapheme(pos)) && (vimWhitespace(cursor.right().grapheme(cursor.right().offset)) || cursor.right().offset >= len(c.text)) {
			return cursor.right().wordMotion("E")
		}
		if vimWhitespace(cursor.grapheme(cursor.offset)) {
			cursor = cursor.wordMotion("W")
		}
		for cursor.offset < len(c.text) && !vimWhitespace(cursor.right().grapheme(cursor.right().offset)) {
			cursor = cursor.right()
		}
		return cursor
	}
	return c
}
func (c vimCursor) find(character []uint16, kind string, count int) (int, bool) {
	forward, till := kind == "f" || kind == "t", kind == "t" || kind == "T"
	pos := c.previous(c.offset)
	if forward {
		pos = c.next(c.offset)
	} else if c.offset == 0 {
		return 0, false
	}
	for pos >= 0 && pos < len(c.text) {
		if vimEqual(c.grapheme(pos), character) {
			count--
			if count == 0 {
				if till {
					if forward {
						return max(c.offset, c.previous(pos)), true
					}
					return min(c.offset, c.next(pos)), true
				}
				return pos, true
			}
		}
		if forward {
			pos = c.next(pos)
		} else {
			if pos == 0 {
				break
			}
			pos = c.previous(pos)
		}
	}
	return 0, false
}

type vimWrappedLine struct {
	Text              []uint16
	Start             int
	Preceded, Newline bool
}

func (c vimCursor) wrapped() []vimWrappedLine {
	// Rendering uses the same existing ANSI/grapheme width dependency. Source
	// Cursor reserves one cell for the cursor, retained by c.columns.
	wrapped := strings.Split(ansi.Wrap(vimDisplay(c.text), c.columns, ""), "\n")
	result := []vimWrappedLine{}
	search, lastNewline := 0, -1
	for i, part := range wrapped {
		units := vimUnits(part)
		start := len(c.text)
		ends := false
		if len(units) == 0 {
			for j := lastNewline + 1; j < len(c.text); j++ {
				if c.text[j] == '\n' {
					start = j
					lastNewline = j
					ends = true
					break
				}
			}
		} else {
			for j := search; j+len(units) <= len(c.text); j++ {
				if vimEqual(c.text[j:j+len(units)], units) {
					start = j
					break
				}
			}
			if i > 0 && search < start && start < len(c.text) {
				gap := c.text[search:start]
				clean := true
				for _, unit := range gap {
					if unit == '\n' || !vimWhitespace([]uint16{unit}) {
						clean = false
						break
					}
				}
				if clean {
					prefix := []uint16{}
					previous := &result[len(result)-1]
					for _, unit := range gap {
						if len(prefix) == 0 && vimCellWidth(previous.Text)+vimCellWidth([]uint16{unit}) <= c.columns {
							previous.Text = append(previous.Text, unit)
						} else {
							prefix = append(prefix, unit)
						}
					}
					if len(prefix) > 0 {
						units = append(prefix, units...)
						start -= len(prefix)
					}
				}
			}
			search = start + len(units)
			ends = search < len(c.text) && c.text[search] == '\n'
			if ends {
				lastNewline = search
			}
		}
		result = append(result, vimWrappedLine{units, start, i == 0 || start > 0 && c.text[start-1] == '\n', ends})
	}
	return result
}
func vimCellWidth(units []uint16) int {
	width := 0
	for _, g := range vimGraphemes(units) {
		width += g.Width
	}
	return width
}
func vimTrimStart(units []uint16) []uint16 {
	for len(units) > 0 && vimWhitespace(units[:1]) {
		units = units[1:]
	}
	return units
}
func (c vimCursor) position() (int, int) {
	lines := c.wrapped()
	for i, line := range lines {
		if c.offset >= line.Start && (i+1 == len(lines) || c.offset < lines[i+1].Start) {
			pos := min(max(c.offset-line.Start, 0), len(line.Text))
			if !line.Preceded {
				leading := len(line.Text) - len(vimTrimStart(line.Text))
				if pos < leading {
					return i, 0
				}
				return i, vimCellWidth(line.Text[leading:pos])
			}
			return i, vimCellWidth(line.Text[:pos])
		}
	}
	return len(lines) - 1, vimCellWidth(lines[len(lines)-1].Text)
}
func (c vimCursor) visual(direction int) vimCursor {
	lines := c.wrapped()
	line, column := c.position()
	target := line + direction
	if target < 0 || target >= len(lines) {
		return c
	}
	current := lines[target]
	visible := current.Text
	if !current.Preceded {
		visible = vimTrimStart(visible)
	}
	return c.visualPosition(target, min(column, vimCellWidth(visible)))
}
func (c vimCursor) motion(key string, count int) vimCursor {
	for i := 0; i < count; i++ {
		next := c
		switch key {
		case "h":
			next = c.left()
		case "l":
			next = c.right()
		case "j":
			next = c.logical(1)
		case "k":
			next = c.logical(-1)
		case "gj":
			next = c.visual(1)
		case "gk":
			next = c.visual(-1)
		case "w", "b", "e", "W", "B", "E":
			next = c.wordMotion(key)
		case "0":
			next = c.at(c.lineStart(c.offset))
		case "^":
			next = c.firstNonBlank()
		case "$":
			next = c.at(c.lineEnd(c.offset))
		case "G":
			next = c.lastLine()
		}
		if next.offset == c.offset {
			break
		}
		c = next
	}
	return c
}
