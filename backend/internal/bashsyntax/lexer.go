// Package bashsyntax ports the original pure bashParser language parser.
// Syntax nodes and tokens retain original UTF-16 text and UTF-8 byte offsets.
// It has no filesystem, environment, subprocess, permission or Harness state.
package bashsyntax

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Node struct {
	Type       string          `json:"type"`
	Text       json.RawMessage `json:"text"`
	StartIndex int             `json:"startIndex"`
	EndIndex   int             `json:"endIndex"`
	Children   []*Node         `json:"children"`
}

type token struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
	Start int             `json:"start"`
	End   int             `json:"end"`
}

type heredocPending struct {
	delim                                []uint16
	stripTabs, quoted                    bool
	bodyStart, bodyEnd, endStart, endEnd int
}
type lexer struct {
	src       []uint16
	i, b      int
	heredocs  []*heredocPending
	byteTable []int
}

func newLexer(input json.RawMessage) (*lexer, error) {
	units, err := protocol.DecodeJavaScriptJSONString(input)
	if err != nil {
		return nil, err
	}
	return &lexer{src: units, heredocs: []*heredocPending{}}, nil
}
func (l *lexer) advance() {
	if l.i >= len(l.src) {
		l.i++
		l.b += 3
		return
	}
	c := l.src[l.i]
	l.i++
	if c < 0x80 {
		l.b++
	} else if c < 0x800 {
		l.b += 2
	} else if c >= 0xD800 && c <= 0xDBFF {
		l.b += 4
		l.i++
	} else {
		l.b += 3
	}
}
func (l *lexer) peek(offset int) int {
	index := l.i + offset
	if index < 0 || index >= len(l.src) {
		return -1
	}
	return int(l.src[index])
}
func (l *lexer) byteAt(index int) int {
	if l.byteTable == nil {
		l.byteTable = make([]int, len(l.src)+1)
		b := 0
		for i := 0; i < len(l.src); {
			l.byteTable[i] = b
			c := l.src[i]
			if c < 0x80 {
				b++
				i++
			} else if c < 0x800 {
				b += 2
				i++
			} else if c >= 0xD800 && c <= 0xDBFF {
				if i+1 < len(l.byteTable) {
					l.byteTable[i+1] = b + 2
				}
				b += 4
				i += 2
			} else {
				b += 3
				i++
			}
		}
		l.byteTable[len(l.src)] = b
	}
	return l.byteTable[index]
}
func (l *lexer) slice(start, end int) json.RawMessage {
	if start < 0 {
		start = 0
	}
	if end > len(l.src) {
		end = len(l.src)
	}
	if start > end {
		start = end
	}
	return protocol.QuoteJavaScriptUTF16(l.src[start:end])
}
func isWordChar(c int) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || containsASCII("_/.-+:@%,~^?*!=[]", c)
}
func containsASCII(chars string, c int) bool {
	for _, v := range chars {
		if int(v) == c {
			return true
		}
	}
	return false
}
func isIdentStart(c int) bool       { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' }
func isIdentChar(c int) bool        { return isIdentStart(c) || isDigit(c) }
func isDigit(c int) bool            { return c >= '0' && c <= '9' }
func isHexDigit(c int) bool         { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
func isBaseDigit(c int) bool        { return isIdentChar(c) || c == '@' }
func isHeredocDelimChar(c int) bool { return c != -1 && !containsASCII(" \t\n<>|&;()'\"`\\", c) }
func (l *lexer) skipBlanks() {
	for l.i < len(l.src) {
		c := l.peek(0)
		if c == ' ' || c == '\t' || c == '\r' {
			l.advance()
		} else if c == '\\' {
			next := l.peek(1)
			if next == '\n' || next == '\r' && l.peek(2) == '\n' {
				l.advance()
				l.advance()
				if next == '\r' {
					l.advance()
				}
			} else if next == ' ' || next == '\t' {
				l.advance()
				l.advance()
			} else {
				break
			}
		} else {
			break
		}
	}
}
func quoteASCII(s string) json.RawMessage {
	units := make([]uint16, 0, len(s))
	for _, c := range s {
		units = append(units, uint16(c))
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func (l *lexer) fixed(kind, value string, count, start int) token {
	for i := 0; i < count; i++ {
		l.advance()
	}
	return token{kind, quoteASCII(value), start, l.b}
}
func (l *lexer) next(position string) token {
	l.skipBlanks()
	start := l.b
	if l.i >= len(l.src) {
		return token{"EOF", quoteASCII(""), start, start}
	}
	c, c1, c2 := l.peek(0), l.peek(1), l.peek(2)
	if c == '\n' {
		return l.fixed("NEWLINE", "\n", 1, start)
	}
	if c == '#' {
		begin := l.i
		for l.i < len(l.src) && l.peek(0) != '\n' {
			l.advance()
		}
		return token{"COMMENT", l.slice(begin, l.i), start, l.b}
	}
	// Original order matters: longest operators precede their prefixes, and
	// process substitution precedes the single-character redirect operator.
	for _, op := range []string{"&&", "||", "|&", ";;&", ";;", ";&", ">>", ">&-", ">&", ">|", "&>>", "&>", "<<<", "<<-", "<<", "<&-", "<&", "<(", ">(", "((", "))"} {
		match := true
		for i, ch := range op {
			if l.peek(i) != int(ch) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		kind := "OP"
		if op == "<(" {
			kind = "LT_PAREN"
		}
		if op == ">(" {
			kind = "GT_PAREN"
		}
		return l.fixed(kind, op, len(op), start)
	}
	if containsASCII("|&;><()", c) {
		return l.fixed("OP", string(rune(c)), 1, start)
	}
	if position == "cmd" {
		if c == '[' && c1 == '[' {
			return l.fixed("OP", "[[", 2, start)
		}
		if c == '[' {
			return l.fixed("OP", "[", 1, start)
		}
		if c == '{' && (c1 == ' ' || c1 == '\t' || c1 == '\n') {
			return l.fixed("OP", "{", 1, start)
		}
		if c == '}' {
			return l.fixed("OP", "}", 1, start)
		}
		if c == '!' && (c1 == ' ' || c1 == '\t') {
			return l.fixed("OP", "!", 1, start)
		}
	}
	if c == '"' {
		return l.fixed("DQUOTE", "\"", 1, start)
	}
	if c == '\'' {
		begin := l.i
		l.advance()
		for l.i < len(l.src) && l.peek(0) != '\'' {
			l.advance()
		}
		if l.i < len(l.src) {
			l.advance()
		}
		return token{"SQUOTE", l.slice(begin, l.i), start, l.b}
	}
	if c == '$' {
		if c1 == '(' && c2 == '(' {
			return l.fixed("DOLLAR_DPAREN", "$((", 3, start)
		}
		if c1 == '(' {
			return l.fixed("DOLLAR_PAREN", "$(", 2, start)
		}
		if c1 == '{' {
			return l.fixed("DOLLAR_BRACE", "${", 2, start)
		}
		if c1 == '\'' {
			begin := l.i
			l.advance()
			l.advance()
			for l.i < len(l.src) && l.peek(0) != '\'' {
				if l.peek(0) == '\\' && l.i+1 < len(l.src) {
					l.advance()
				}
				l.advance()
			}
			if l.i < len(l.src) {
				l.advance()
			}
			return token{"ANSI_C", l.slice(begin, l.i), start, l.b}
		}
		return l.fixed("DOLLAR", "$", 1, start)
	}
	if c == '`' {
		return l.fixed("BACKTICK", "`", 1, start)
	}
	if isDigit(c) {
		j := l.i
		for j < len(l.src) && isDigit(int(l.src[j])) {
			j++
		}
		after := -1
		if j < len(l.src) {
			after = int(l.src[j])
		}
		if after == '>' || after == '<' {
			begin := l.i
			for l.i < j {
				l.advance()
			}
			return token{"WORD", l.slice(begin, l.i), start, l.b}
		}
	}
	if isWordChar(c) || c == '\\' || c == '{' || c == '}' {
		begin := l.i
		for l.i < len(l.src) {
			ch := l.peek(0)
			if ch == '\\' {
				if l.i+1 >= len(l.src) {
					break
				}
				l.advance()
				l.advance()
				continue
			}
			if !isWordChar(ch) && ch != '{' && ch != '}' {
				break
			}
			l.advance()
		}
		if l.i > begin {
			kind := "WORD"
			digits := begin
			if l.src[digits] == '-' {
				digits++
			}
			number := digits < l.i
			for j := digits; j < l.i; j++ {
				if j >= len(l.src) || !isDigit(int(l.src[j])) {
					number = false
					break
				}
			}
			if number {
				kind = "NUMBER"
			}
			return token{kind, l.slice(begin, l.i), start, l.b}
		}
	}
	l.advance()
	return token{"WORD", protocol.QuoteJavaScriptUTF16([]uint16{uint16(c)}), start, l.b}
}
