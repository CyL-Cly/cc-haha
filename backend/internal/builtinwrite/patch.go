package builtinwrite

import (
	"strings"
	"time"
)

type PatchHunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}
type diffComponent struct {
	count          int
	added, removed bool
	previous       *diffComponent
}
type diffPath struct {
	oldPos    int
	component *diffComponent
}
type diffLines struct {
	added, removed bool
	lines          []string
}

func lineTokens(text string) []string {
	out := []string{}
	for len(text) > 0 {
		n := strings.IndexByte(text, '\n')
		if n < 0 {
			out = append(out, text)
			break
		}
		out = append(out, text[:n+1])
		text = text[n+1:]
	}
	return out
}
func addDiff(path *diffPath, added, removed bool, increment int) *diffPath {
	c := path.component
	next := &diffComponent{count: 1, added: added, removed: removed, previous: c}
	if c != nil && c.added == added && c.removed == removed {
		next.count = c.count + 1
		next.previous = c.previous
	}
	return &diffPath{path.oldPos + increment, next}
}
func commonDiff(path *diffPath, old, new []string, diagonal int) int {
	oldPos := path.oldPos
	newPos := oldPos - diagonal
	n := 0
	for oldPos+1 < len(old) && newPos+1 < len(new) && old[oldPos+1] == new[newPos+1] {
		oldPos++
		newPos++
		n++
	}
	if n > 0 {
		path.component = &diffComponent{count: n, previous: path.component}
	}
	path.oldPos = oldPos
	return newPos
}
func buildDiff(c *diffComponent, old, new []string) []diffLines {
	parts := []*diffComponent{}
	for c != nil {
		parts = append(parts, c)
		c = c.previous
	}
	out := []diffLines{}
	o, n := 0, 0
	for i := len(parts) - 1; i >= 0; i-- {
		c := parts[i]
		var lines []string
		if c.removed {
			lines = old[o : o+c.count]
			o += c.count
		} else {
			lines = new[n : n+c.count]
			n += c.count
			if !c.added {
				o += c.count
			}
		}
		out = append(out, diffLines{c.added, c.removed, lines})
	}
	return out
}

// StructuredPatch ports diff.js's line-token Myers path selection and context-3
// hunk construction used by Source getPatchFromContents. A timed-out diff keeps
// Source's empty-patch behavior; it does not change the actual replacement.
func StructuredPatch(oldText, newText string) []PatchHunk {
	return structuredPatch(oldText, newText, 5*time.Second)
}

// StructuredPatchUnbounded supplies Source checkpoint rewind's untimed
// createTwoFilesPatch diff. The write tool retains its original five seconds.
func StructuredPatchUnbounded(oldText, newText string) []PatchHunk {
	return structuredPatch(oldText, newText, 0)
}

func structuredPatch(oldText, newText string, timeout time.Duration) []PatchHunk {
	old, new := lineTokens(oldText), lineTokens(newText)
	first := &diffPath{oldPos: -1}
	p := commonDiff(first, old, new, 0)
	best := map[int]*diffPath{0: first}
	var changes []diffLines
	if first.oldPos+1 >= len(old) && p+1 >= len(new) {
		changes = buildDiff(first.component, old, new)
	} else {
		min, max := -len(old)-len(new), len(old)+len(new)
		deadline := time.Now().Add(timeout)
	outer:
		for length := 1; length <= len(old)+len(new) && (timeout == 0 || !time.Now().After(deadline)); length++ {
			start, end := -length, length
			if min > start {
				start = min
			}
			if max < end {
				end = max
			}
			for diagonal := start; diagonal <= end; diagonal += 2 {
				remove, add := best[diagonal-1], best[diagonal+1]
				delete(best, diagonal-1)
				canAdd := add != nil && add.oldPos-diagonal >= 0 && add.oldPos-diagonal < len(new)
				canRemove := remove != nil && remove.oldPos+1 < len(old)
				if !canAdd && !canRemove {
					delete(best, diagonal)
					continue
				}
				var base *diffPath
				if !canRemove || (canAdd && remove.oldPos < add.oldPos) {
					base = addDiff(add, true, false, 0)
				} else {
					base = addDiff(remove, false, true, 1)
				}
				np := commonDiff(base, old, new, diagonal)
				if base.oldPos+1 >= len(old) && np+1 >= len(new) {
					changes = buildDiff(base.component, old, new)
					break outer
				}
				best[diagonal] = base
				if base.oldPos+1 >= len(old) && diagonal-1 < max {
					max = diagonal - 1
				}
				if np+1 >= len(new) && diagonal+1 > min {
					min = diagonal + 1
				}
			}
		}
	}
	if changes == nil {
		return []PatchHunk{}
	}
	changes = append(changes, diffLines{})
	hunks := []PatchHunk{}
	oldStart, newStart, oldLine, newLine := 0, 0, 1, 1
	current := []string{}
	contextLines := func(lines []string) []string {
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = " " + l
		}
		return out
	}
	for i, c := range changes {
		if c.added || c.removed {
			if oldStart == 0 {
				oldStart, newStart = oldLine, newLine
				if i > 0 {
					prev := changes[i-1].lines
					if len(prev) > 3 {
						prev = prev[len(prev)-3:]
					}
					current = contextLines(prev)
					oldStart -= len(prev)
					newStart -= len(prev)
				}
			}
			prefix := "-"
			if c.added {
				prefix = "+"
			}
			for _, l := range c.lines {
				current = append(current, prefix+l)
			}
			if c.added {
				newLine += len(c.lines)
			} else {
				oldLine += len(c.lines)
			}
		} else {
			if oldStart != 0 {
				if len(c.lines) <= 6 && i < len(changes)-2 {
					current = append(current, contextLines(c.lines)...)
				} else {
					n := len(c.lines)
					if n > 3 {
						n = 3
					}
					current = append(current, contextLines(c.lines[:n])...)
					hunks = append(hunks, PatchHunk{oldStart, oldLine - oldStart + n, newStart, newLine - newStart + n, current})
					oldStart, newStart = 0, 0
					current = []string{}
				}
			}
			oldLine += len(c.lines)
			newLine += len(c.lines)
		}
	}
	for i := range hunks {
		lines := []string{}
		for _, l := range hunks[i].Lines {
			if strings.HasSuffix(l, "\n") {
				lines = append(lines, strings.TrimSuffix(l, "\n"))
			} else {
				lines = append(lines, l, `\ No newline at end of file`)
			}
		}
		hunks[i].Lines = lines
	}
	return hunks
}

func ConvertLeadingTabsToSpaces(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		n := len(line) - len(strings.TrimLeft(line, "\t"))
		lines[i] = strings.Repeat("  ", n) + line[n:]
	}
	return strings.Join(lines, "\n")
}
