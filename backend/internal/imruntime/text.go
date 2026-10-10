package imruntime

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Text retains JavaScript's UTF-16 offsets through IM limits and wire JSON.
type Text []uint16

func NewText(s string) Text { return utf16.Encode([]rune(s)) }
func TextJSON(raw json.RawMessage) Text {
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	return units
}
func (s Text) MarshalJSON() ([]byte, error) { return protocol.QuoteJavaScriptUTF16(s), nil }
func (s Text) String() string               { return string(utf16.Decode(s)) }
func (s Text) Clone() Text                  { return append(Text{}, s...) }
func (s Text) Join(other Text) Text         { return append(s.Clone(), other...) }
func (s Text) Slice(start, end int) Text {
	if start < 0 {
		start = 0
	}
	if end > len(s) {
		end = len(s)
	}
	if start > end {
		start = end
	}
	return s[start:end].Clone()
}
func (s Text) TrimStart() Text {
	start := 0
	for start < len(s) && protocol.IsJavaScriptWhitespace(rune(s[start])) {
		start++
	}
	return s.Slice(start, len(s))
}
func (s Text) TrimEnd() Text {
	end := len(s)
	for end > 0 && protocol.IsJavaScriptWhitespace(rune(s[end-1])) {
		end--
	}
	return s.Slice(0, end)
}
func (s Text) Trim() Text { return s.TrimStart().TrimEnd() }
func (s Text) LastIndex(needle Text, limit int) int {
	if limit > len(s)-len(needle) {
		limit = len(s) - len(needle)
	}
	for i := limit; i >= 0; i-- {
		equal := true
		for j, v := range needle {
			if s[i+j] != v {
				equal = false
				break
			}
		}
		if equal {
			return i
		}
	}
	return -1
}
func (s Text) HasPrefix(prefix Text) bool {
	return len(s) >= len(prefix) && s.LastIndex(prefix, 0) == 0
}
func (s Text) Split(delimiter uint16) []Text {
	result := []Text{}
	start := 0
	for i, v := range s {
		if v == delimiter {
			result = append(result, s.Slice(start, i))
			start = i + 1
		}
	}
	return append(result, s.Slice(start, len(s)))
}
func JoinText(values []Text, delimiter Text) Text {
	result := Text{}
	for i, value := range values {
		if i > 0 {
			result = append(result, delimiter...)
		}
		result = append(result, value...)
	}
	return result
}

func SplitMessage(text Text, limit int) []Text {
	if len(text) <= limit || limit <= 0 {
		return []Text{text.Clone()}
	}
	result := []Text{}
	remaining := text.Clone()
	for len(remaining) > 0 {
		if len(remaining) <= limit {
			result = append(result, remaining)
			break
		}
		split := remaining.LastIndex(NewText("\n\n"), limit)
		for _, delimiter := range []string{"\n", ". ", " "} {
			if split <= 0 {
				split = remaining.LastIndex(NewText(delimiter), limit)
			}
		}
		if split <= 0 {
			split = limit
		}
		if remaining[split] == '\n' || remaining[split] == '.' {
			split++
		}
		result = append(result, remaining.Slice(0, split).TrimEnd())
		remaining = remaining.Slice(split, len(remaining)).TrimStart()
	}
	return result
}
func tableCells(line Text) []Text {
	line = line.Trim()
	if len(line) > 0 && line[0] == '|' {
		line = line.Slice(1, len(line))
	}
	if len(line) > 0 && line[len(line)-1] == '|' {
		line = line.Slice(0, len(line)-1)
	}
	cells := line.Split('|')
	for i := range cells {
		cells[i] = cells[i].Trim()
	}
	return cells
}
func tableRow(line Text) bool {
	return line.LastIndex(NewText("|"), len(line)) >= 0 && len(tableCells(line)) >= 2
}
func tableDivider(line Text) bool {
	cells := tableCells(line)
	if len(cells) < 2 {
		return false
	}
	for _, cell := range cells {
		value := cell.String()
		value = strings.TrimPrefix(value, ":")
		value = strings.TrimSuffix(value, ":")
		if len(value) < 3 || strings.Trim(value, "-") != "" {
			return false
		}
	}
	return true
}
func fence(line Text) bool {
	value := line.TrimStart().String()
	return strings.HasPrefix(value, "```") || strings.HasPrefix(value, "~~~")
}
func ConvertMarkdownTables(text Text) Text {
	lines := text.Split('\n')
	out := []Text{}
	inFence := false
	for i := 0; i < len(lines); {
		header := lines[i]
		if fence(header) {
			inFence = !inFence
			out = append(out, header)
			i++
			continue
		}
		if !inFence && i+1 < len(lines) && tableRow(header) && tableDivider(lines[i+1]) {
			headers := tableCells(header)
			i += 2
			rows := [][]Text{}
			for i < len(lines) && tableRow(lines[i]) {
				rows = append(rows, tableCells(lines[i]))
				i++
			}
			rendered := []Text{}
			for _, row := range rows {
				nonempty := false
				for _, v := range row {
					nonempty = nonempty || len(v) > 0
				}
				if !nonempty {
					continue
				}
				if len(row[0]) > 0 {
					rendered = append(rendered, row[0])
				}
				for j := 1; j < len(row); j++ {
					if len(row[j]) == 0 {
						continue
					}
					label := NewText("Column " + stringNumber(j))
					if j < len(headers) && len(headers[j]) > 0 {
						label = headers[j]
					}
					rendered = append(rendered, NewText("• ").Join(label).Join(NewText(": ")).Join(row[j]))
				}
				if len(rendered) > 0 && len(rendered[len(rendered)-1]) > 0 {
					rendered = append(rendered, Text{})
				}
			}
			for len(rendered) > 0 && len(rendered[len(rendered)-1]) == 0 {
				rendered = rendered[:len(rendered)-1]
			}
			if len(rendered) > 0 {
				out = append(out, JoinText(rendered, NewText("\n")))
			}
			continue
		}
		out = append(out, header)
		i++
	}
	return JoinText(out, NewText("\n"))
}

func PreviewInput(input json.RawMessage, limit int) Text {
	var text Text
	if len(input) > 0 && input[0] == '"' {
		text = TextJSON(input)
	} else {
		compact, err := protocol.ParseAndStringifyJavaScriptJSON(input)
		if err != nil {
			return NewText("(unserializable)")
		}
		// Indentation preserves JSON.stringify's ordered keys and string escapes.
		var result bytes.Buffer
		if err = json.Indent(&result, compact, "", "  "); err != nil {
			return NewText("(unserializable)")
		}
		text = NewText(result.String())
	}
	if len(text) > limit {
		return text.Slice(0, limit).Join(NewText("…"))
	}
	return text
}
