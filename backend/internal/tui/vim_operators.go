package tui

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"strings"
)

func (v *VimEditor) cursor(columns int) vimCursor { return newVimCursor(v.Text, columns, v.Offset) }
func (v *VimEditor) apply(operator string, from, to int, linewise bool) {
	from, to = min(max(from, 0), len(v.Text)), min(max(to, 0), len(v.Text))
	content := vimClone(v.Text[from:to])
	if linewise && (len(content) == 0 || content[len(content)-1] != '\n') {
		content = append(content, '\n')
	}
	v.Register, v.RegisterIsLinewise = content, linewise
	if operator == "yank" {
		v.Offset = from
		return
	}
	v.change(vimSplice(v.Text, from, to, nil))
	if operator == "change" {
		v.enterInsert(from)
	} else {
		v.Offset = min(from, max(0, len(v.Text)-max(1, len(vimLast(v.Text)))))
	}
}
func (v *VimEditor) operatorRange(cursor, target vimCursor, motion, operator string, count int) (int, int, bool) {
	from, to := min(cursor.offset, target.offset), max(cursor.offset, target.offset)
	linewise := false
	if operator == "change" && (motion == "w" || motion == "W") {
		wordCursor := cursor
		for i := 0; i < count-1; i++ {
			wordCursor = wordCursor.wordMotion(motion)
		}
		end := wordCursor.wordMotion(map[string]string{"w": "e", "W": "E"}[motion])
		to = cursor.next(end.offset)
	} else if strings.Contains("jkG", motion) || motion == "gg" {
		linewise = true
		next := cursor.lineEnd(to)
		if next == len(cursor.text) {
			to = len(cursor.text)
			if from > 0 && cursor.text[from-1] == '\n' {
				from--
			}
		} else {
			to = next + 1
		}
	} else if strings.Contains("eE$", motion) && cursor.offset <= target.offset {
		to = cursor.next(to)
	}
	return cursor.imageSnap(from, false), cursor.imageSnap(to, true), linewise
}
func (v *VimEditor) operatorMotion(operator, motion string, count, columns int) {
	cursor := v.cursor(columns)
	target := cursor.motion(motion, count)
	if cursor.offset == target.offset {
		return
	}
	from, to, linewise := v.operatorRange(cursor, target, motion, operator, count)
	v.apply(operator, from, to, linewise)
	v.record(vimRecordedChange{Type: "operator", Operator: operator, Motion: motion, Count: count})
}
func (v *VimEditor) operatorFind(operator, find string, character []uint16, count, columns int) {
	cursor := v.cursor(columns)
	offset, ok := cursor.find(character, find, count)
	if !ok {
		return
	}
	v.apply(operator, min(cursor.offset, offset), cursor.next(max(cursor.offset, offset)), false)
	v.LastFind = &vimFind{find, vimClone(character)}
	v.record(vimRecordedChange{Type: "operatorFind", Operator: operator, Find: find, Character: vimClone(character), Count: count})
}
func (v *VimEditor) operatorObject(operator, scope, kind string, count int) {
	rangeValue := vimTextObject(v.Text, v.Offset, kind, scope == "inner")
	if rangeValue == nil {
		return
	}
	v.apply(operator, rangeValue.Start, rangeValue.End, false)
	v.record(vimRecordedChange{Type: "operatorTextObj", Operator: operator, Scope: scope, Object: kind, Count: count})
}
func vimSplitLines(text []uint16) [][]uint16 {
	result := [][]uint16{}
	start := 0
	for i, value := range text {
		if value == '\n' {
			result = append(result, vimClone(text[start:i]))
			start = i + 1
		}
	}
	return append(result, vimClone(text[start:]))
}
func vimJoinLines(lines [][]uint16) []uint16 {
	result := []uint16{}
	for i, line := range lines {
		if i > 0 {
			result = append(result, '\n')
		}
		result = append(result, line...)
	}
	return result
}
func vimLineOffset(lines [][]uint16, index int) int {
	prefix := min(max(index, 0), len(lines))
	offset := 0
	for i := 0; i < prefix; i++ {
		offset += len(lines[i])
		if i > 0 {
			offset++
		}
	}
	if index > 0 {
		offset++
	}
	return offset
}
func (v *VimEditor) lineOperator(operator string, count, columns int) {
	cursor := v.cursor(columns)
	lines := vimSplitLines(v.Text)
	current := 0
	for _, unit := range v.Text[:min(cursor.offset, len(v.Text))] {
		if unit == '\n' {
			current++
		}
	}
	affected := min(count, len(lines)-current)
	start := cursor.lineStart(cursor.offset)
	end := start
	for i := 0; i < affected; i++ {
		end = cursor.lineEnd(end)
		if end < len(v.Text) {
			end++
		}
	}
	content := vimClone(v.Text[start:end])
	if len(content) == 0 || content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	v.Register, v.RegisterIsLinewise = content, true
	switch operator {
	case "yank":
		v.Offset = start
	case "delete":
		if end == len(v.Text) && start > 0 && v.Text[start-1] == '\n' {
			start--
		}
		v.change(vimSplice(v.Text, start, end, nil))
		v.Offset = min(start, max(0, len(v.Text)-max(1, len(vimLast(v.Text)))))
	case "change":
		if len(lines) == 1 {
			v.change(nil)
			v.enterInsert(0)
		} else {
			next := append(append([][]uint16{}, lines[:current]...), []uint16{})
			next = append(next, lines[current+affected:]...)
			v.change(vimJoinLines(next))
			v.enterInsert(start)
		}
	}
	v.record(vimRecordedChange{Type: "operator", Operator: operator, Motion: operator[:1], Count: count})
}
func (v *VimEditor) deleteCharacters(count, columns int) {
	cursor := v.cursor(columns)
	from := cursor.offset
	if from >= len(v.Text) {
		return
	}
	end := cursor
	for i := 0; i < count && end.offset < len(end.text); i++ {
		end = end.right()
	}
	v.Register, v.RegisterIsLinewise = vimClone(v.Text[from:min(end.offset, len(v.Text))]), false
	v.change(vimSplice(v.Text, from, end.offset, nil))
	v.Offset = min(from, max(0, len(v.Text)-max(1, len(vimLast(v.Text)))))
	v.record(vimRecordedChange{Type: "x", Count: count})
}
func (v *VimEditor) replace(character []uint16, count int) {
	offset := v.cursor(80).offset
	text := vimClone(v.Text)
	for i := 0; i < count && offset < len(text); i++ {
		length := max(1, len(vimFirst(text[offset:])))
		text = vimSplice(text, offset, offset+length, character)
		offset += len(character)
	}
	v.change(text)
	v.Offset = max(0, offset-len(character))
	v.record(vimRecordedChange{Type: "replace", Character: vimClone(character), Count: count})
}
func (v *VimEditor) toggle(count int) {
	startOffset := v.cursor(80).offset
	if startOffset >= len(v.Text) {
		return
	}
	text, offset := vimClone(v.Text), startOffset
	for i := 0; i < count && offset < len(text); i++ {
		value := vimFirst(text[offset:])
		display := vimDisplay(value)
		replacement := cases.Upper(language.Und).String(display)
		if replacement == display {
			replacement = cases.Lower(language.Und).String(display)
		}
		units := vimUnits(replacement)
		if replacement == display {
			units = vimClone(value)
		}
		text = vimSplice(text, offset, offset+len(value), units)
		offset += len(units)
	}
	v.change(text)
	v.Offset = offset
	v.record(vimRecordedChange{Type: "toggleCase", Count: count})
}
func (v *VimEditor) join(count, columns int) {
	lines := vimSplitLines(v.Text)
	current, _ := v.cursor(columns).position()
	if current >= len(lines)-1 {
		return
	}
	joinCount := min(count, len(lines)-current-1)
	joined := vimClone(lines[current])
	cursor := len(joined)
	for i := 1; i <= joinCount; i++ {
		next := vimTrimStart(lines[current+i])
		if len(next) > 0 {
			if len(joined) > 0 && joined[len(joined)-1] != ' ' {
				joined = append(joined, ' ')
			}
			joined = append(joined, next...)
		}
	}
	next := append(append([][]uint16{}, lines[:current]...), joined)
	next = append(next, lines[current+joinCount+1:]...)
	v.change(vimJoinLines(next))
	v.Offset = vimLineOffset(next, current) + cursor
	v.record(vimRecordedChange{Type: "join", Count: count})
}
func (v *VimEditor) paste(after bool, count, columns int) {
	if len(v.Register) == 0 {
		return
	}
	linewise := v.Register[len(v.Register)-1] == '\n'
	if linewise {
		lines := vimSplitLines(v.Text)
		current, _ := v.cursor(columns).position()
		insert := current
		if after {
			insert++
		}
		actualInsert := min(max(insert, 0), len(lines))
		content := vimSplitLines(v.Register[:len(v.Register)-1])
		repeated := [][]uint16{}
		for i := 0; i < count; i++ {
			repeated = append(repeated, content...)
		}
		next := append(append([][]uint16{}, lines[:actualInsert]...), repeated...)
		next = append(next, lines[actualInsert:]...)
		v.change(vimJoinLines(next))
		v.Offset = vimLineOffset(next, insert)
	} else {
		content := []uint16{}
		for i := 0; i < count; i++ {
			content = append(content, v.Register...)
		}
		insert := v.Offset
		if after && insert < len(v.Text) {
			insert = v.cursor(columns).next(insert)
		}
		v.change(vimSplice(v.Text, insert, insert, content))
		v.Offset = max(insert, insert+len(content)-max(1, len(vimLast(content))))
	}
}
func (v *VimEditor) indent(direction string, count, columns int) {
	lines := vimSplitLines(v.Text)
	current, _ := v.cursor(columns).position()
	affected := min(count, len(lines)-current)
	for i := 0; i < affected; i++ {
		index := current + i
		line := lines[index]
		if direction == ">" {
			lines[index] = append([]uint16{' ', ' '}, line...)
		} else {
			remove := 0
			if len(line) > 0 && line[0] == '\t' {
				remove = 1
			} else {
				for remove < min(2, len(line)) && vimWhitespace(line[remove:remove+1]) {
					remove++
				}
			}
			lines[index] = line[remove:]
		}
	}
	v.change(vimJoinLines(lines))
	v.Offset = vimLineOffset(lines, current)
	if current < len(lines) {
		v.Offset += len(lines[current]) - len(vimTrimStart(lines[current]))
	}
	v.record(vimRecordedChange{Type: "indent", Direction: direction, Count: count})
}
func (v *VimEditor) openLine(direction string, columns int) {
	lines := vimSplitLines(v.Text)
	current, _ := v.cursor(columns).position()
	insert := current
	if direction == "below" {
		insert++
	}
	actualInsert := min(max(insert, 0), len(lines))
	next := append(append([][]uint16{}, lines[:actualInsert]...), []uint16{})
	next = append(next, lines[actualInsert:]...)
	v.change(vimJoinLines(next))
	v.enterInsert(vimLineOffset(next, insert))
	v.record(vimRecordedChange{Type: "openLine", Direction: direction})
}
func (v *VimEditor) operatorG(operator, motion string, count, columns int) {
	cursor := v.cursor(columns)
	target := cursor.goLine(count)
	if count == 1 {
		if motion == "G" {
			target = cursor.lastLine()
		} else {
			target = cursor.at(0)
		}
	}
	if target.offset == cursor.offset {
		return
	}
	from, to, linewise := v.operatorRange(cursor, target, motion, operator, count)
	v.apply(operator, from, to, linewise)
	v.record(vimRecordedChange{Type: "operator", Operator: operator, Motion: motion, Count: count})
}
func (v *VimEditor) replay(columns int) {
	if v.LastChange == nil {
		return
	}
	change := *v.LastChange
	v.Replay = true
	defer func() { v.Replay = false }()
	switch change.Type {
	case "insert":
		if len(change.Text) > 0 {
			cursor := v.cursor(columns)
			v.change(vimNFC(vimSplice(cursor.text, cursor.offset, cursor.offset, change.Text)))
			v.Offset = cursor.offset + len(vimNFC(change.Text))
		}
	case "x":
		v.deleteCharacters(change.Count, columns)
	case "replace":
		v.replace(change.Character, change.Count)
	case "toggleCase":
		v.toggle(change.Count)
	case "indent":
		v.indent(change.Direction, change.Count, columns)
	case "join":
		v.join(change.Count, columns)
	case "openLine":
		v.openLine(change.Direction, columns)
	case "operator":
		v.operatorMotion(change.Operator, change.Motion, change.Count, columns)
	case "operatorFind":
		v.operatorFind(change.Operator, change.Find, change.Character, change.Count, columns)
	case "operatorTextObj":
		v.operatorObject(change.Operator, change.Scope, change.Object, change.Count)
	}
}
