package terminalsetup

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

type jsonToken struct {
	kind       byte
	start, end int
	raw        json.RawMessage
	invalid    bool
}

// JSONC uses a structured token stream: comment delimiters inside strings are
// never removed, and each array child retains its physical source span.
type jsonNode struct {
	kind       byte
	start, end int
	raw        json.RawMessage
	children   []jsonNode
}

func parseJSONC(text string) (jsonNode, error) {
	tokens := jsonTokens(text)
	i := 0
	var parse func() (jsonNode, error)
	skipTrivia := func() {
		for i < len(tokens) && (tokens[i].kind == 'l' || tokens[i].kind == 'b' || tokens[i].kind == 'u') {
			i++
		}
	}
	current := func() jsonToken {
		skipTrivia()
		if i == len(tokens) {
			return jsonToken{kind: 'e', start: len(text), end: len(text)}
		}
		return tokens[i]
	}
	recover := func(closing byte) {
		for token := current(); token.kind != 'e' && token.kind != closing && token.kind != ','; token = current() {
			i++
		}
	}
	parse = func() (jsonNode, error) {
		token := current()
		node := jsonNode{kind: token.kind, start: token.start, end: token.end}
		if token.kind != '[' && token.kind != '{' {
			if token.kind != 's' && token.kind != 'v' && token.kind != 'n' {
				return node, errors.New("missing JSONC value")
			}
			node.raw = token.raw
			i++
			return node, nil
		}
		i++
		closing := byte(']')
		if token.kind == '{' {
			closing = '}'
		}
		var raw bytes.Buffer
		raw.WriteByte(token.kind)
		values := 0
		for {
			next := current()
			if next.kind == closing || next.kind == 'e' {
				node.end = next.end
				if next.kind == closing {
					i++
				}
				raw.WriteByte(closing)
				node.raw = raw.Bytes()
				return node, nil
			}
			if next.kind == ',' {
				i++
				next = current()
				if next.kind == closing {
					continue
				}
			}
			var key jsonNode
			if token.kind == '{' {
				if next.kind != 's' {
					recover(closing)
					continue
				}
				key, _ = parse()
				if current().kind != ':' {
					recover(closing)
					continue
				}
				i++
			}
			child, err := parse()
			if err != nil {
				recover(closing)
				continue
			}
			if values > 0 {
				raw.WriteByte(',')
			}
			values++
			if token.kind == '{' {
				raw.Write(key.raw)
				raw.WriteByte(':')
			}
			node.children = append(node.children, child)
			raw.Write(child.raw)
		}
	}
	return parse()
}

type jsonEdit struct {
	start, end int
	text       string
}

func jsonEOL(text string) string {
	for i := 0; i < len(text); i++ {
		if text[i] == '\r' {
			if i+1 < len(text) && text[i+1] == '\n' {
				return "\r\n"
			}
			return "\r"
		}
		if text[i] == '\n' {
			return "\n"
		}
	}
	return "\n"
}

// The insertion range and whitespace rules follow Microsoft's MIT-licensed
// jsonc-parser formatter. Only the existing array-append operation is exposed.
func formatJSONCRange(text string, begin, end int) string {
	start, stop := begin, end
	for start > 0 && text[start-1] != '\r' && text[start-1] != '\n' {
		start--
	}
	for stop < len(text) && text[stop] != '\r' && text[stop] != '\n' {
		stop++
	}
	fragment := text[start:stop]
	indent := 0
	for _, character := range fragment {
		if character == ' ' {
			indent++
		} else if character == '\t' {
			indent += 4
		} else {
			break
		}
	}
	initial, level := indent/4, 0
	tokens := append(jsonTokens(fragment), jsonToken{kind: 'e', start: len(fragment), end: len(fragment)})
	eol := jsonEOL(text)
	var edits []jsonEdit
	hasError := tokens[0].kind == 'u' || tokens[0].invalid
	add := func(value string, first, last int) {
		first += start
		last += start
		if !hasError && first < end && last > begin && text[first:last] != value {
			edits = append(edits, jsonEdit{first, last, value})
		}
	}
	newline := func() string { return eol + strings.Repeat("    ", max(0, initial+level)) }
	if tokens[0].kind != 'e' {
		add(strings.Repeat("    ", initial), 0, tokens[0].start)
	}
	for i := 0; i < len(tokens)-1; i++ {
		first := tokens[i]
		second := tokens[i+1]
		hasError = second.kind == 'u' || second.invalid
		last := first.end
		value := ""
		needsLine := false
		for !strings.ContainsAny(fragment[last:second.start], "\r\n") && (second.kind == 'l' || second.kind == 'b') {
			add(" ", last, second.start)
			last = second.end
			needsLine = second.kind == 'l'
			if needsLine {
				value = newline()
			} else {
				value = ""
			}
			i++
			second = tokens[i+1]
			hasError = second.kind == 'u' || second.invalid
		}
		line := strings.ContainsAny(fragment[last:second.start], "\r\n")
		if second.kind == '}' || second.kind == ']' {
			opening := byte('{')
			if second.kind == ']' {
				opening = '['
			}
			if first.kind != opening {
				level--
				value = newline()
			}
		} else {
			switch first.kind {
			case '[', '{':
				level++
				value = newline()
			case ',', 'l':
				value = newline()
			case 'b':
				if line {
					value = newline()
				} else if !needsLine {
					value = " "
				}
			case ':':
				if !needsLine {
					value = " "
				}
			case 's':
				if second.kind == ':' && !needsLine {
					value = ""
				}
			case 'v', 'n', '}', ']':
				if (second.kind == 'l' || second.kind == 'b') && !needsLine {
					value = " "
				} else if second.kind != ',' && second.kind != 'e' {
					hasError = true
				}
			case 'u':
				hasError = true
			}
			if line && (second.kind == 'l' || second.kind == 'b') {
				value = newline()
			}
		}
		if second.kind == 'e' {
			value = ""
		}
		add(value, last, second.start)
	}
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		text = text[:edit.start] + edit.text + text[edit.end:]
	}
	return text
}

func appendJSONC(content string, item json.RawMessage) string {
	fallback := func() string {
		var buffer bytes.Buffer
		_ = json.Indent(&buffer, append(append([]byte{'['}, item...), ']'), "", "    ")
		return buffer.String()
	}
	text := strings.TrimPrefix(content, "\ufeff")
	node, err := parseJSONC(text)
	if err != nil || node.kind != '[' {
		return fallback()
	}
	position := node.start + 1
	insertion := string(item)
	if len(node.children) > 0 {
		position = node.children[len(node.children)-1].end
		insertion = "," + insertion
	}
	text = text[:position] + insertion + text[position:]
	begin, end := position, position+len(insertion)
	for begin > 0 && text[begin-1] != '\r' && text[begin-1] != '\n' {
		begin--
	}
	for end < len(text) && text[end] != '\r' && text[end] != '\n' {
		end++
	}
	return formatJSONCRange(text, begin, end)
}
