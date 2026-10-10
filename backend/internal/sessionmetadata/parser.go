package sessionmetadata

import (
	"errors"
	"unicode/utf8"
)

type syntaxFrame struct {
	object bool
	state  byte
}
type tokenizer struct {
	stack                    []syntaxFrame
	done                     bool
	mode                     byte
	key                      bool
	escape                   byte
	hex                      uint16
	digits                   int
	number                   byte
	literal                  string
	literalIndex             int
	pending                  [4]byte
	pendingSize, pendingWant int
	tokens                   []token
}

func syntaxError() error                 { return errors.New("invalid JSON") }
func (p *tokenizer) emit(kind tokenKind) { p.tokens = append(p.tokens, token{kind: kind}) }
func (p *tokenizer) raw(kind tokenKind, raw []byte) {
	if len(raw) != 0 {
		p.tokens = append(p.tokens, token{kind: kind, raw: raw})
	}
}
func (p *tokenizer) unit(unit uint16) {
	p.tokens = append(p.tokens, token{kind: stringChunk, units: [2]uint16{unit}, count: 1})
}
func (p *tokenizer) rune(r rune) {
	if r <= 0xffff {
		p.unit(uint16(r))
		return
	}
	r -= 0x10000
	p.tokens = append(p.tokens, token{kind: stringChunk, units: [2]uint16{uint16(0xd800 + (r >> 10)), uint16(0xdc00 + (r & 0x3ff))}, count: 2})
}
func (p *tokenizer) complete() {
	if len(p.stack) == 0 {
		p.done = true
		return
	}
	top := &p.stack[len(p.stack)-1]
	if top.object {
		top.state = 4
	} else {
		top.state = 7
	}
}
func isDigit(b byte) bool { return b >= '0' && b <= '9' }
func white(b byte) bool   { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }
func hexDigit(b byte) (uint16, bool) {
	switch {
	case b >= '0' && b <= '9':
		return uint16(b - '0'), true
	case b >= 'a' && b <= 'f':
		return uint16(b - 'a' + 10), true
	case b >= 'A' && b <= 'F':
		return uint16(b - 'A' + 10), true
	}
	return 0, false
}
func sequenceSize(b byte) int {
	if b >= 0xc2 && b <= 0xdf {
		return 2
	}
	if b >= 0xe0 && b <= 0xef {
		return 3
	}
	if b >= 0xf0 && b <= 0xf4 {
		return 4
	}
	return 0
}
func validContinuation(lead byte, index int, b byte) bool {
	lower, upper := byte(0x80), byte(0xbf)
	if index == 1 {
		if lead == 0xe0 {
			lower = 0xa0
		}
		if lead == 0xed {
			upper = 0x9f
		}
		if lead == 0xf0 {
			lower = 0x90
		}
		if lead == 0xf4 {
			upper = 0x8f
		}
	}
	return b >= lower && b <= upper
}
func (p *tokenizer) stringBytes(data []byte, index int) (int, error) {
	b := data[index]
	if p.pendingSize != 0 {
		if !validContinuation(p.pending[0], p.pendingSize, b) {
			p.unit(0xfffd)
			p.pendingSize = 0
			return index, nil
		}
		p.pending[p.pendingSize] = b
		p.pendingSize++
		if p.pendingSize == p.pendingWant {
			r, _ := utf8.DecodeRune(p.pending[:p.pendingSize])
			p.rune(r)
			p.pendingSize = 0
		}
		return index + 1, nil
	}
	if p.escape == 2 {
		digit, valid := hexDigit(b)
		if !valid {
			return index, syntaxError()
		}
		p.hex = p.hex<<4 | digit
		p.digits++
		if p.digits == 4 {
			p.unit(p.hex)
			p.escape = 0
		}
		return index + 1, nil
	}
	if p.escape == 1 {
		p.escape = 0
		switch b {
		case 'u':
			p.escape = 2
			p.hex = 0
			p.digits = 0
		case 'b':
			p.unit('\b')
		case 'f':
			p.unit('\f')
		case 'n':
			p.unit('\n')
		case 'r':
			p.unit('\r')
		case 't':
			p.unit('\t')
		case '"', '\\', '/':
			p.unit(uint16(b))
		default:
			return index, syntaxError()
		}
		return index + 1, nil
	}
	if b == '"' {
		p.mode = 0
		if p.key {
			p.emit(endKey)
			p.stack[len(p.stack)-1].state = 2
		} else {
			p.emit(endString)
			p.complete()
		}
		return index + 1, nil
	}
	if b == '\\' {
		p.escape = 1
		return index + 1, nil
	}
	if b < 0x20 {
		return index, syntaxError()
	}
	if b < 0x80 {
		end := index + 1
		for end < len(data) && data[end] >= 0x20 && data[end] < 0x80 && data[end] != '"' && data[end] != '\\' {
			end++
		}
		p.raw(stringChunk, data[index:end])
		return end, nil
	}
	want := sequenceSize(b)
	if want == 0 {
		p.unit(0xfffd)
		return index + 1, nil
	}
	count := 1
	for count < want && index+count < len(data) && validContinuation(b, count, data[index+count]) {
		count++
	}
	if count == want {
		r, _ := utf8.DecodeRune(data[index : index+count])
		p.rune(r)
	} else if index+count == len(data) {
		copy(p.pending[:], data[index:index+count])
		p.pendingSize = count
		p.pendingWant = want
	} else {
		p.unit(0xfffd)
	}
	return index + count, nil
}
func (p *tokenizer) numberByte(b byte) (bool, error) {
	switch p.number {
	case '-':
		if !isDigit(b) {
			return false, syntaxError()
		}
		if b == '0' {
			p.number = '0'
		} else {
			p.number = 'i'
		}
	case '0', 'i':
		if isDigit(b) && p.number == 'i' {
			return true, nil
		}
		if b == '.' {
			p.number = '.'
		} else if b == 'e' || b == 'E' {
			p.number = 'e'
		} else {
			return false, nil
		}
	case '.':
		if !isDigit(b) {
			return false, syntaxError()
		}
		p.number = 'f'
	case 'f':
		if isDigit(b) {
			return true, nil
		}
		if b == 'e' || b == 'E' {
			p.number = 'e'
		} else {
			return false, nil
		}
	case 'e':
		if b == '+' || b == '-' {
			p.number = '+'
		} else if isDigit(b) {
			p.number = 'x'
		} else {
			return false, syntaxError()
		}
	case '+':
		if !isDigit(b) {
			return false, syntaxError()
		}
		p.number = 'x'
	case 'x':
		if !isDigit(b) {
			return false, nil
		}
	}
	return true, nil
}
func (p *tokenizer) finishNumber() error {
	if p.number == '-' || p.number == '.' || p.number == 'e' || p.number == '+' {
		return syntaxError()
	}
	p.emit(endNumber)
	p.mode = 0
	p.complete()
	return nil
}
func (p *tokenizer) valueByte(b byte) error {
	switch b {
	case '{', '[':
		kind := startObject
		state := byte(0)
		if b == '[' {
			kind = startArray
			state = 5
		}
		p.emit(kind)
		p.stack = append(p.stack, syntaxFrame{object: b == '{', state: state})
	case '"':
		p.mode = '"'
		p.key = false
		p.emit(startString)
	case 't', 'f', 'n':
		p.mode = 'l'
		p.literalIndex = 1
		if b == 't' {
			p.literal = "true"
		} else if b == 'f' {
			p.literal = "false"
		} else {
			p.literal = "null"
		}
	default:
		if b != '-' && !isDigit(b) {
			return syntaxError()
		}
		p.mode = 'n'
		p.number = 'i'
		if b == '-' || b == '0' {
			p.number = b
		}
		p.emit(startNumber)
		p.tokens = append(p.tokens, token{kind: numberChunk, units: [2]uint16{uint16(b)}, count: 1})
	}
	return nil
}
func (p *tokenizer) structural(b byte) error {
	if white(b) {
		return nil
	}
	if len(p.stack) == 0 {
		if p.done {
			return syntaxError()
		}
		return p.valueByte(b)
	}
	top := &p.stack[len(p.stack)-1]
	switch top.state {
	case 0, 1:
		if b == '}' && top.state == 0 {
			p.emit(endObject)
			p.stack = p.stack[:len(p.stack)-1]
			p.complete()
			return nil
		}
		if b != '"' {
			return syntaxError()
		}
		p.mode = '"'
		p.key = true
		p.emit(startKey)
	case 2:
		if b != ':' {
			return syntaxError()
		}
		top.state = 3
	case 3, 6:
		return p.valueByte(b)
	case 5:
		if b == ']' {
			p.emit(endArray)
			p.stack = p.stack[:len(p.stack)-1]
			p.complete()
			return nil
		}
		return p.valueByte(b)
	case 4, 7:
		if b == ',' {
			if top.object {
				top.state = 1
			} else {
				top.state = 6
			}
			return nil
		}
		if top.object && b == '}' || !top.object && b == ']' {
			if top.object {
				p.emit(endObject)
			} else {
				p.emit(endArray)
			}
			p.stack = p.stack[:len(p.stack)-1]
			p.complete()
			return nil
		}
		return syntaxError()
	}
	return nil
}

// Tokens from one input write become visible only after that write parses
// successfully, matching stream-json's batch before projection callbacks.
func (p *tokenizer) feed(data []byte, final bool) ([]token, error) {
	for index := range p.tokens {
		p.tokens[index] = token{}
	}
	p.tokens = p.tokens[:0]
	for index := 0; index < len(data); {
		b := data[index]
		switch p.mode {
		case '"':
			next, err := p.stringBytes(data, index)
			if err != nil {
				return nil, err
			}
			index = next
		case 'n':
			start := index
			for index < len(data) {
				accepted, err := p.numberByte(data[index])
				if err != nil {
					return nil, err
				}
				if !accepted {
					break
				}
				index++
			}
			p.raw(numberChunk, data[start:index])
			if index < len(data) {
				if err := p.finishNumber(); err != nil {
					return nil, err
				}
			}
		case 'l':
			if p.literalIndex < len(p.literal) {
				if b != p.literal[p.literalIndex] {
					return nil, syntaxError()
				}
				p.literalIndex++
				index++
				continue
			}
			if isDigit(b) || b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' {
				return nil, syntaxError()
			}
			kind := nullValue
			if p.literal == "true" {
				kind = trueValue
			} else if p.literal == "false" {
				kind = falseValue
			}
			p.emit(kind)
			p.mode = 0
			p.complete()
		default:
			if err := p.structural(b); err != nil {
				return nil, err
			}
			index++
		}
	}
	if final {
		if p.mode == 'n' {
			if err := p.finishNumber(); err != nil {
				return nil, err
			}
		}
		if p.mode == 'l' && p.literalIndex == len(p.literal) {
			kind := nullValue
			if p.literal == "true" {
				kind = trueValue
			} else if p.literal == "false" {
				kind = falseValue
			}
			p.emit(kind)
			p.mode = 0
			p.complete()
		}
		if p.mode != 0 || len(p.stack) != 0 || !p.done {
			return nil, syntaxError()
		}
	}
	return p.tokens, nil
}
