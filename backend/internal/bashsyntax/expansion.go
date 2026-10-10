package bashsyntax

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

func (p *parseState) expansionBody() []*Node {
	out := []*Node{}
	p.l.skipBlanks()
	c0, c1 := p.l.peek(0), p.l.peek(1)
	if c0 == '#' && c1 == '!' && p.l.peek(2) == '}' {
		p.l.advance()
		p.l.advance()
		return out
	}
	if c0 == '!' && c1 == '#' {
		j := 2
		if p.l.peek(j) == '#' {
			j++
		}
		if p.l.peek(j) == ' ' {
			j++
		}
		if p.l.peek(j) == '}' {
			for j > 0 {
				p.l.advance()
				j--
			}
			return out
		}
	}
	if p.l.peek(0) == '#' {
		s := p.l.b
		p.l.advance()
		out = append(out, p.node("#", s, p.l.b))
	}
	c := p.l.peek(0)
	if (c == '!' || c == '=' || c == '~') && (isIdentStart(p.l.peek(1)) || isDigit(p.l.peek(1))) {
		s := p.l.b
		p.l.advance()
		out = append(out, p.node(string(rune(c)), s, p.l.b))
	}
	p.l.skipBlanks()
	c = p.l.peek(0)
	if isIdentStart(c) {
		s := p.l.b
		for isIdentChar(p.l.peek(0)) {
			p.l.advance()
		}
		out = append(out, p.node("variable_name", s, p.l.b))
	} else if isDigit(c) {
		s := p.l.b
		for isDigit(p.l.peek(0)) {
			p.l.advance()
		}
		out = append(out, p.node("variable_name", s, p.l.b))
	} else if containsASCII("?$@*#-!_", c) {
		s := p.l.b
		p.l.advance()
		out = append(out, p.node("special_variable_name", s, p.l.b))
	}
	if p.l.peek(0) == '[' {
		var variable *Node
		if len(out) > 0 {
			variable = out[len(out)-1]
		}
		s := p.l.b
		p.l.advance()
		open := p.node("[", s, p.l.b)
		index := p.subscriptIndex()
		p.l.skipBlanks()
		s = p.l.b
		if p.l.peek(0) == ']' {
			p.l.advance()
		}
		close := p.node("]", s, p.l.b)
		if variable != nil {
			kids := []*Node{variable, open}
			if index != nil {
				kids = append(kids, index)
			}
			kids = append(kids, close)
			out[len(out)-1] = p.node("subscript", variable.StartIndex, p.l.b, kids...)
		}
	}
	p.l.skipBlanks()
	c = p.l.peek(0)
	if (c == '*' || c == '@') && p.l.peek(1) == '}' {
		s := p.l.b
		p.l.advance()
		out = append(out, p.node(string(rune(c)), s, p.l.b))
		return out
	}
	if c == '@' && isIdentStart(p.l.peek(1)) {
		s := p.l.b
		p.l.advance()
		out = append(out, p.node("@", s, p.l.b))
		for isIdentChar(p.l.peek(0)) {
			p.l.advance()
		}
		return out
	}
	if c == ':' {
		next := p.l.peek(1)
		if next == '\n' || next == '}' {
			p.l.advance()
			for p.l.peek(0) == '\n' {
				p.l.advance()
			}
			return out
		}
		if !containsASCII("-=?+", next) {
			p.l.advance()
			p.l.skipBlanks()
			offset := p.substringNumber(":}")
			if offset != nil {
				out = append(out, offset)
			}
			p.l.skipBlanks()
			if p.l.peek(0) == ':' {
				p.l.advance()
				p.l.skipBlanks()
				length := p.substringNumber("}")
				if length != nil {
					out = append(out, length)
				}
			}
			return out
		}
	}
	if containsASCII(":#%/^,-=?+", c) {
		s := p.l.b
		next := p.l.peek(1)
		op := string(rune(c))
		p.l.advance()
		if c == ':' && containsASCII("-=?+", next) || containsASCII("#%/^,", c) && next == c {
			p.l.advance()
			op += string(rune(next))
		}
		out = append(out, p.node(op, s, p.l.b))
		pattern := op == "#" || op == "##" || op == "%" || op == "%%" || op == "/" || op == "//" || op == "^" || op == "^^" || op == "," || op == ",,"
		if op == "/" || op == "//" {
			if anchor := p.l.peek(0); anchor == '#' || anchor == '%' {
				s := p.l.b
				p.l.advance()
				out = append(out, p.node(string(rune(anchor)), s, p.l.b))
			}
			if p.l.peek(0) == '"' {
				out = append(out, p.doubleQuoted())
				if rest := p.expansionRest("regex", true); rest != nil {
					out = append(out, rest)
				}
			} else if rest := p.expansionRest("regex", true); rest != nil {
				out = append(out, rest)
			}
			if p.l.peek(0) == '/' {
				s := p.l.b
				p.l.advance()
				out = append(out, p.node("/", s, p.l.b))
				if rest := p.expansionRest("replword", false); rest != nil {
					if rest.Type == "concatenation" && len(rest.Children) == 2 && rest.Children[0].Type == "command_substitution" {
						out = append(out, rest.Children...)
					} else {
						out = append(out, rest)
					}
				}
			}
		} else if op == "#" || op == "##" || op == "%" || op == "%%" {
			out = append(out, p.expansionRegexSegments()...)
		} else {
			kind := "word"
			if pattern {
				kind = "regex"
			}
			if rest := p.expansionRest(kind, false); rest != nil {
				out = append(out, rest)
			}
		}
	}
	return out
}
func (p *parseState) opaqueExpansion() bool {
	if p.l.peek(0) != '$' {
		return false
	}
	next := p.l.peek(1)
	if next != '{' && next != '(' {
		return false
	}
	open, close := next, '}'
	if open == '(' {
		close = ')'
	}
	depth := 1
	p.l.advance()
	p.l.advance()
	for p.l.i < len(p.l.src) && depth > 0 {
		c := p.l.peek(0)
		if c == open {
			depth++
		} else if c == int(close) {
			depth--
		}
		p.l.advance()
	}
	return true
}
func (p *parseState) expansionRest(kind string, stopAtSlash bool) *Node {
	start := p.l.b
	if kind == "word" && p.l.peek(0) == '(' {
		p.l.advance()
		open := p.node("(", start, p.l.b)
		elems := []*Node{open}
		for p.l.i < len(p.l.src) {
			p.l.skipBlanks()
			c := p.l.peek(0)
			if c == ')' || c == '}' || c == '\n' || c == -1 {
				break
			}
			s := p.l.b
			for p.l.i < len(p.l.src) {
				if next := p.l.peek(0); next == -1 || containsASCII(")} \t\n", next) {
					break
				}
				p.l.advance()
			}
			if p.l.b > s {
				elems = append(elems, p.node("word", s, p.l.b))
			} else {
				break
			}
		}
		if p.l.peek(0) == ')' {
			s := p.l.b
			p.l.advance()
			elems = append(elems, p.node(")", s, p.l.b))
		}
		for p.l.peek(0) == '\n' {
			p.l.advance()
		}
		return p.node("array", start, p.l.b, elems...)
	}
	if kind == "regex" {
		depth := 0
		for p.l.i < len(p.l.src) {
			c := p.l.peek(0)
			if c == '\n' || depth == 0 && (c == '}' || stopAtSlash && c == '/') {
				break
			}
			if c == '\\' && p.l.i+1 < len(p.l.src) {
				p.l.advance()
				p.l.advance()
				continue
			}
			if c == '"' || c == '\'' {
				p.l.advance()
				for p.l.i < len(p.l.src) && p.l.peek(0) != c {
					if p.l.peek(0) == '\\' && p.l.i+1 < len(p.l.src) {
						p.l.advance()
					}
					p.l.advance()
				}
				if p.l.peek(0) == c {
					p.l.advance()
				}
				continue
			}
			if p.opaqueExpansion() {
				continue
			}
			if c == '{' {
				depth++
			} else if c == '}' && depth > 0 {
				depth--
			}
			p.l.advance()
		}
		end := p.l.b
		for p.l.peek(0) == '\n' {
			p.l.advance()
		}
		if end == start {
			return nil
		}
		return p.node("regex", start, end)
	}
	parts := []*Node{}
	segStart, depth := p.l.b, 0
	flush := func() {
		if p.l.b > segStart {
			parts = append(parts, p.node("word", segStart, p.l.b))
		}
	}
	for p.l.i < len(p.l.src) {
		c, next := p.l.peek(0), p.l.peek(1)
		if c == '\n' || depth == 0 && (c == '}' || stopAtSlash && c == '/') {
			break
		}
		if c == '\\' && p.l.i+1 < len(p.l.src) {
			p.l.advance()
			p.l.advance()
			continue
		}
		if c == '$' {
			if next == '{' || next == '(' || next == '[' || isIdentStart(next) || isDigit(next) || containsASCII("?$@*#-!_", next) {
				flush()
				if exp := p.dollar(); exp != nil {
					parts = append(parts, exp)
				}
				segStart = p.l.b
				continue
			}
			if next == '\'' {
				flush()
				parts = append(parts, p.leaf("ansi_c_string", p.l.next("arg")))
				segStart = p.l.b
				continue
			}
		}
		if c == '"' {
			flush()
			parts = append(parts, p.doubleQuoted())
			segStart = p.l.b
			continue
		}
		if c == '\'' {
			flush()
			parts = append(parts, p.leaf("raw_string", p.l.next("arg")))
			segStart = p.l.b
			continue
		}
		if (c == '<' || c == '>') && next == '(' {
			flush()
			if sub := p.processSubstitution(); sub != nil {
				parts = append(parts, sub)
			}
			segStart = p.l.b
			continue
		}
		if c == '`' {
			flush()
			if sub := p.backtick(); sub != nil {
				parts = append(parts, sub)
			}
			segStart = p.l.b
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' && depth > 0 {
			depth--
		}
		p.l.advance()
	}
	flush()
	for p.l.peek(0) == '\n' {
		p.l.advance()
	}
	if len(parts) > 1 && parts[0].Type == "word" && blankWord(parts[0]) {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return p.node("concatenation", parts[0].StartIndex, parts[len(parts)-1].EndIndex, parts...)
}
func blankWord(n *Node) bool {
	units, err := protocol.DecodeJavaScriptJSONString(n.Text)
	if err != nil || len(units) == 0 {
		return false
	}
	for _, c := range units {
		if c != ' ' && c != '\t' {
			return false
		}
	}
	return true
}
func (p *parseState) expansionRegexSegments() []*Node {
	out := []*Node{}
	start := p.l.b
	flush := func() {
		if p.l.b > start {
			out = append(out, p.node("regex", start, p.l.b))
		}
	}
	for p.l.i < len(p.l.src) {
		c := p.l.peek(0)
		if c == '}' || c == '\n' {
			break
		}
		if c == '\\' && p.l.i+1 < len(p.l.src) {
			p.l.advance()
			p.l.advance()
			continue
		}
		if c == '"' {
			flush()
			out = append(out, p.doubleQuoted())
			start = p.l.b
			continue
		}
		if c == '\'' {
			flush()
			out = append(out, p.leaf("raw_string", p.l.next("arg")))
			start = p.l.b
			continue
		}
		if p.opaqueExpansion() {
			continue
		}
		p.l.advance()
	}
	flush()
	for p.l.peek(0) == '\n' {
		p.l.advance()
	}
	return out
}

func (p *parseState) substringNumber(stop string) *Node {
	if p.l.peek(0) == '-' && isDigit(p.l.peek(1)) {
		s := p.l.b
		p.l.advance()
		for isDigit(p.l.peek(0)) {
			p.l.advance()
		}
		return p.node("number", s, p.l.b)
	}
	return p.arithmeticTernary(stop, "var")
}
