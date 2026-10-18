package builtinwrite

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

type WordChange struct {
	Value   string `json:"value"`
	Added   bool   `json:"added,omitempty"`
	Removed bool   `json:"removed,omitempty"`
}

func wordCharacter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == 0xad || r >= 0xc0 && r <= 0xd6 || r >= 0xd8 && r <= 0xf6 || r >= 0xf8 && r <= 0x2c6 || r >= 0x2c8 && r <= 0x2d7 || r >= 0x2de && r <= 0x2ff || r >= 0x1e00 && r <= 0x1eff
}
func wordTokens(value string) []string {
	r := []rune(value)
	out := []string{}
	for i := 0; i < len(r); {
		j := i + 1
		if r[i] == '\r' && j < len(r) && r[j] == '\n' {
			j++
		} else if wordCharacter(r[i]) {
			for j < len(r) && wordCharacter(r[j]) {
				j++
			}
		} else if r[i] != '\r' && r[i] != '\n' && protocol.IsJavaScriptWhitespace(r[i]) {
			for j < len(r) && r[j] != '\r' && r[j] != '\n' && protocol.IsJavaScriptWhitespace(r[j]) {
				j++
			}
		}
		out = append(out, string(r[i:j]))
		i = j
	}
	return out
}

// WordDiff uses the same diff.js Myers path as StructuredPatch, with the
// frozen diffWordsWithSpace tokenizer and no whitespace/case normalization.
func WordDiff(before, after string) []WordChange {
	old, next := wordTokens(before), wordTokens(after)
	first := &diffPath{oldPos: -1}
	p := commonDiff(first, old, next, 0)
	best := map[int]*diffPath{0: first}
	var changes []diffLines
	if first.oldPos+1 >= len(old) && p+1 >= len(next) {
		changes = buildDiff(first.component, old, next)
	} else {
		minD, maxD := -len(old)-len(next), len(old)+len(next)
	outer:
		for length := 1; length <= len(old)+len(next); length++ {
			start, end := -length, length
			if minD > start {
				start = minD
			}
			if maxD < end {
				end = maxD
			}
			for diagonal := start; diagonal <= end; diagonal += 2 {
				remove, add := best[diagonal-1], best[diagonal+1]
				delete(best, diagonal-1)
				canAdd := add != nil && add.oldPos-diagonal >= 0 && add.oldPos-diagonal < len(next)
				canRemove := remove != nil && remove.oldPos+1 < len(old)
				if !canAdd && !canRemove {
					delete(best, diagonal)
					continue
				}
				var base *diffPath
				if !canRemove || canAdd && remove.oldPos < add.oldPos {
					base = addDiff(add, true, false, 0)
				} else {
					base = addDiff(remove, false, true, 1)
				}
				np := commonDiff(base, old, next, diagonal)
				if base.oldPos+1 >= len(old) && np+1 >= len(next) {
					changes = buildDiff(base.component, old, next)
					break outer
				}
				best[diagonal] = base
				if base.oldPos+1 >= len(old) && diagonal-1 < maxD {
					maxD = diagonal - 1
				}
				if np+1 >= len(next) && diagonal+1 > minD {
					minD = diagonal + 1
				}
			}
		}
	}
	out := []WordChange{}
	for _, part := range changes {
		out = append(out, WordChange{strings.Join(part.lines, ""), part.added, part.removed})
	}
	return out
}
