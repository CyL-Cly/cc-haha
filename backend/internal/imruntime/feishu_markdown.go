package imruntime

import (
	"fmt"
	"github.com/dlclark/regexp2/v2"
	"strconv"
)

func feishuRunes(t Text) []rune {
	r := make([]rune, len(t))
	for i, v := range t {
		r[i] = rune(v)
	}
	return r
}
func feishuGroup(text Text, m *regexp2.Match, n int) Text {
	g := m.GroupByNumber(n)
	if g == nil {
		return Text{}
	}
	return text.Slice(g.RuneIndex, g.RuneIndex+g.RuneLength)
}
func feishuReplace(text Text, pattern string, multi, global bool, replacement func(Text, *regexp2.Match) Text) Text {
	options := regexp2.ECMAScript
	if multi {
		options |= regexp2.Multiline
	}
	re, e := regexp2.Compile(pattern, options)
	if e != nil {
		return text
	}
	m, e := re.FindRunesMatch(feishuRunes(text))
	if e != nil {
		return text
	}
	out := Text{}
	cursor := 0
	for m != nil {
		out = out.Join(text.Slice(cursor, m.RuneIndex)).Join(replacement(text, m))
		cursor = m.RuneIndex + m.RuneLength
		if !global {
			break
		}
		m, e = re.FindNextMatch(m)
		if e != nil {
			return text
		}
	}
	return out.Join(text.Slice(cursor, len(text)))
}
func feishuReplaceString(t Text, p string, multi, global bool, template string) Text {
	return feishuReplace(t, p, multi, global, func(text Text, m *regexp2.Match) Text {
		out := Text{}
		literal := ""
		for i := 0; i < len(template); i++ {
			if template[i] == '$' && i+1 < len(template) && template[i+1] >= '0' && template[i+1] <= '9' {
				out = out.Join(NewText(literal))
				literal = ""
				n := int(template[i+1] - '0')
				out = out.Join(feishuGroup(text, m, n))
				i++
			} else {
				literal += string(template[i])
			}
		}
		return out.Join(NewText(literal))
	})
}
func FeishuOptimizeMarkdown(text Text, version int) Text {
	blocks := []Text{}
	r := feishuReplace(text, "```[\\s\\S]*?```", false, true, func(t Text, m *regexp2.Match) Text {
		blocks = append(blocks, feishuGroup(t, m, 0))
		return NewText(fmt.Sprintf("___CB_%d___", len(blocks)-1))
	})
	heading := regexp2.MustCompile(`^#{1,3} `, regexp2.ECMAScript|regexp2.Multiline)
	match, _ := heading.FindRunesMatch(feishuRunes(text))
	if match != nil {
		r = feishuReplaceString(r, `^#{2,6} (.+)$`, true, true, "##### $1")
		r = feishuReplaceString(r, `^# (.+)$`, true, true, "#### $1")
	}
	if version >= 2 {
		r = feishuReplaceString(r, `^(#{4,5} .+)\n{1,2}(#{4,5} )`, true, true, "$1\n<br>\n$2")
		r = feishuReplaceString(r, `^([^|\n].*)\n(\|.+\|)`, true, true, "$1\n\n$2")
		r = feishuReplaceString(r, `\n\n((?:\|.+\|[^\S\n]*\n?)+)`, false, true, "\n\n<br>\n\n$1")
		original := r.Clone()
		r = feishuReplace(r, `((?:^\|.+\|[^\S\n]*\n?)+)`, true, true, func(t Text, m *regexp2.Match) Text {
			body := feishuGroup(t, m, 0)
			after := feishuReplaceString(original.Slice(m.RuneIndex+m.RuneLength, len(original)), `^\n+`, false, false, "")
			if len(after) == 0 {
				return body
			}
			check := regexp2.MustCompile(`^(---|#{4,5} |\*\*)`, regexp2.ECMAScript)
			match, _ := check.FindRunesMatch(feishuRunes(after))
			if match != nil {
				return body
			}
			return body.Join(NewText("\n<br>\n"))
		})
		r = feishuReplaceString(r, `^((?!#{4,5} )(?!\*\*).+)\n\n(<br>)\n\n(\|)`, true, true, "$1\n$2\n$3")
		r = feishuReplaceString(r, `^(\*\*.+)\n\n(<br>)\n\n(\|)`, true, true, "$1\n$2\n\n$3")
		r = feishuReplaceString(r, `(\|[^\n]*\n)\n(<br>\n)((?!#{4,5} )(?!\*\*))`, true, true, "$1$2$3")
	}
	r = feishuReplaceString(r, `\n{3,}`, false, true, "\n\n")
	for i, block := range blocks {
		replacement := block
		if version >= 2 {
			replacement = NewText("\n<br>\n").Join(block).Join(NewText("\n<br>\n"))
		}
		marker := regexp2.Escape("___CB_" + strconv.Itoa(i) + "___")
		r = feishuReplace(r, marker, false, false, func(Text, *regexp2.Match) Text { return replacement })
	}
	return feishuReplace(r, `!\[([^\]]*)\]\(([^)\s]+)\)`, false, true, func(t Text, m *regexp2.Match) Text {
		if feishuGroup(t, m, 2).HasPrefix(NewText("img_")) {
			return feishuGroup(t, m, 0)
		}
		return Text{}
	})
}

type FeishuMarkdownTable struct {
	Index, Length int
	Raw           Text
}

func FeishuFindMarkdownTables(text Text) []FeishuMarkdownTable {
	ranges := []FeishuMarkdownTable{}
	feishuReplace(text, "```[\\s\\S]*?```", false, true, func(t Text, m *regexp2.Match) Text {
		ranges = append(ranges, FeishuMarkdownTable{m.RuneIndex, m.RuneLength, feishuGroup(t, m, 0)})
		return feishuGroup(t, m, 0)
	})
	matches := []FeishuMarkdownTable{}
	feishuReplace(text, `\|.+\|[\r\n]+\|[-:| ]+\|[\s\S]*?(?=\n\n|\n(?!\|)|$)`, false, true, func(t Text, m *regexp2.Match) Text {
		inside := false
		for _, r := range ranges {
			if m.RuneIndex >= r.Index && m.RuneIndex < r.Index+r.Length {
				inside = true
				break
			}
		}
		if !inside {
			matches = append(matches, FeishuMarkdownTable{m.RuneIndex, m.RuneLength, feishuGroup(t, m, 0)})
		}
		return feishuGroup(t, m, 0)
	})
	return matches
}
func FeishuSanitizeCardText(text Text, limit int) Text {
	matches := FeishuFindMarkdownTables(text)
	if len(matches) <= limit {
		return text
	}
	if limit < 0 {
		limit = 0
	}
	out := text.Clone()
	for i := len(matches) - 1; i >= limit; i-- {
		m := matches[i]
		out = out.Slice(0, m.Index).Join(NewText("```\n")).Join(m.Raw).Join(NewText("\n```")).Join(out.Slice(m.Index+m.Length, len(out)))
	}
	return out
}
