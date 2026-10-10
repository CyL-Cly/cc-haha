package bashsyntax

func (p *parseState) testCommand(t token) *Node {
	open := p.leaf(tokenText(t), t)
	closer := "]]"
	if tokenText(t) == "[" {
		closer = "]"
	}
	save := p.l.save()
	expr := p.testOr(closer)
	p.l.skipBlanks()
	if closer == "]" && p.l.peek(0) != ']' {
		p.l.restore(save)
		previous := p.stopToken
		p.stopToken = "]"
		statement := p.command()
		p.stopToken = previous
		if statement != nil && statement.Type == "redirected_statement" {
			expr = statement
		} else {
			p.l.restore(save)
			expr = p.testOr(closer)
		}
		p.l.skipBlanks()
	}
	closing := p.l.next("arg")
	var close *Node
	if tokenText(closing) == closer {
		close = p.leaf(closer, closing)
	} else {
		close = p.node(closer, open.EndIndex, open.EndIndex)
	}
	kids := []*Node{open}
	if expr != nil {
		kids = append(kids, expr)
	}
	kids = append(kids, close)
	return p.node("test_command", open.StartIndex, close.EndIndex, kids...)
}

func (p *parseState) testOr(closer string) *Node {
	left := p.testAnd(closer)
	if left == nil {
		return nil
	}
	for {
		p.l.skipBlanks()
		save := p.l.save()
		if p.l.peek(0) != '|' || p.l.peek(1) != '|' {
			break
		}
		s := p.l.b
		p.l.advance()
		p.l.advance()
		op := p.node("||", s, p.l.b)
		right := p.testAnd(closer)
		if right == nil {
			p.l.restore(save)
			break
		}
		left = p.node("binary_expression", left.StartIndex, right.EndIndex, left, op, right)
	}
	return left
}
func (p *parseState) testAnd(closer string) *Node {
	left := p.testUnary(closer)
	if left == nil {
		return nil
	}
	for {
		p.l.skipBlanks()
		if p.l.peek(0) != '&' || p.l.peek(1) != '&' {
			break
		}
		s := p.l.b
		p.l.advance()
		p.l.advance()
		op := p.node("&&", s, p.l.b)
		right := p.testUnary(closer)
		if right == nil {
			break
		}
		left = p.node("binary_expression", left.StartIndex, right.EndIndex, left, op, right)
	}
	return left
}
func (p *parseState) testUnary(closer string) *Node {
	p.l.skipBlanks()
	if p.l.peek(0) != '(' {
		return p.testBinary(closer)
	}
	s := p.l.b
	p.l.advance()
	open := p.node("(", s, p.l.b)
	inner := p.testOr(closer)
	p.l.skipBlanks()
	s = p.l.b
	if p.l.peek(0) == ')' {
		p.l.advance()
	}
	close := p.node(")", s, p.l.b)
	kids := []*Node{open}
	if inner != nil {
		kids = append(kids, inner)
	}
	kids = append(kids, close)
	return p.node("parenthesized_expression", open.StartIndex, close.EndIndex, kids...)
}
func (p *parseState) testNegatable(closer string) *Node {
	p.l.skipBlanks()
	c := p.l.peek(0)
	if c == '!' {
		s := p.l.b
		p.l.advance()
		bang := p.node("!", s, p.l.b)
		inner := p.testNegatable(closer)
		if inner == nil {
			return bang
		}
		return p.node("unary_expression", bang.StartIndex, inner.EndIndex, bang, inner)
	}
	if c == '-' && isIdentStart(p.l.peek(1)) {
		s := p.l.b
		p.l.advance()
		for isIdentChar(p.l.peek(0)) {
			p.l.advance()
		}
		op := p.node("test_operator", s, p.l.b)
		p.l.skipBlanks()
		arg := p.testPrimary(closer)
		if arg == nil {
			return op
		}
		return p.node("unary_expression", op.StartIndex, arg.EndIndex, op, arg)
	}
	return p.testPrimary(closer)
}
func (p *parseState) testBinary(closer string) *Node {
	p.l.skipBlanks()
	left := p.testNegatable(closer)
	if left == nil {
		return nil
	}
	p.l.skipBlanks()
	c, c1 := p.l.peek(0), p.l.peek(1)
	s := p.l.b
	kind := ""
	if c == '=' && c1 == '=' {
		kind = "=="
		p.l.advance()
		p.l.advance()
	} else if c == '!' && c1 == '=' {
		kind = "!="
		p.l.advance()
		p.l.advance()
	} else if c == '=' && c1 == '~' {
		kind = "=~"
		p.l.advance()
		p.l.advance()
	} else if c == '=' && c1 != '=' {
		kind = "="
		p.l.advance()
	} else if c == '<' && c1 != '<' {
		kind = "<"
		p.l.advance()
	} else if c == '>' && c1 != '>' {
		kind = ">"
		p.l.advance()
	} else if c == '-' && isIdentStart(c1) {
		kind = "test_operator"
		p.l.advance()
		for isIdentChar(p.l.peek(0)) {
			p.l.advance()
		}
	}
	if kind == "" {
		return left
	}
	op := p.node(kind, s, p.l.b)
	p.l.skipBlanks()
	if closer == "]]" {
		if kind == "=~" {
			var right *Node
			c = p.l.peek(0)
			if c == '"' || c == '\'' {
				save := p.l.save()
				var quote *Node
				if c == '"' {
					quote = p.doubleQuoted()
				} else {
					quote = p.leaf("raw_string", p.l.next("arg"))
				}
				if p.testPatternEnds(true) {
					right = quote
				} else {
					p.l.restore(save)
				}
			}
			if right == nil {
				right = p.testRegex()
			}
			if right == nil {
				return left
			}
			return p.node("binary_expression", left.StartIndex, right.EndIndex, left, op, right)
		}
		if kind == "=" {
			right := p.testRegex()
			if right == nil {
				return left
			}
			return p.node("binary_expression", left.StartIndex, right.EndIndex, left, op, right)
		}
		if kind == "==" || kind == "!=" {
			parts := p.testExtglob()
			if len(parts) == 0 {
				return left
			}
			return p.node("binary_expression", left.StartIndex, parts[len(parts)-1].EndIndex, append([]*Node{left, op}, parts...)...)
		}
	}
	right := p.testPrimary(closer)
	if right == nil {
		return left
	}
	return p.node("binary_expression", left.StartIndex, right.EndIndex, left, op, right)
}

// Only the wholly quoted RHS accepts EOF/newline as a terminal; the raw
// pattern scan deliberately includes ordinary spaces and trailing blanks.
func (p *parseState) testPatternEnds(quoted bool) bool {
	j := p.l.i
	for j < len(p.l.src) && (p.l.src[j] == ' ' || p.l.src[j] == '\t') {
		j++
	}
	c, c1 := -1, -1
	if j < len(p.l.src) {
		c = int(p.l.src[j])
	}
	if j+1 < len(p.l.src) {
		c1 = int(p.l.src[j+1])
	}
	return c == ']' && c1 == ']' || c == '&' && c1 == '&' || c == '|' && c1 == '|' || quoted && (c == '\n' || c == -1)
}
func (p *parseState) testRegex() *Node {
	p.l.skipBlanks()
	s := p.l.b
	parens, brackets := 0, 0
	for p.l.i < len(p.l.src) {
		c := p.l.peek(0)
		if c == '\\' && p.l.i+1 < len(p.l.src) {
			p.l.advance()
			p.l.advance()
			continue
		}
		if c == '\n' {
			break
		}
		if parens == 0 && brackets == 0 {
			if c == ']' && p.l.peek(1) == ']' {
				break
			}
			if c == ' ' || c == '\t' {
				if p.testPatternEnds(false) {
					break
				}
				p.l.advance()
				continue
			}
		}
		if c == '(' {
			parens++
		} else if c == ')' && parens > 0 {
			parens--
		} else if c == '[' {
			brackets++
		} else if c == ']' && brackets > 0 {
			brackets--
		}
		p.l.advance()
	}
	if s == p.l.b {
		return nil
	}
	return p.node("regex", s, p.l.b)
}
func (p *parseState) testExtglob() []*Node {
	p.l.skipBlanks()
	parts := []*Node{}
	start, startI := p.l.b, p.l.i
	parens := 0
	flush := func() {
		if p.l.i > startI {
			kind := "number"
			for _, c := range p.l.src[startI:p.l.i] {
				if !isDigit(int(c)) {
					kind = "extglob_pattern"
					break
				}
			}
			parts = append(parts, p.node(kind, start, p.l.b))
		}
	}
	for p.l.i < len(p.l.src) {
		c := p.l.peek(0)
		if c == '\\' && p.l.i+1 < len(p.l.src) {
			p.l.advance()
			p.l.advance()
			continue
		}
		if c == '\n' {
			break
		}
		if parens == 0 {
			if c == ']' && p.l.peek(1) == ']' {
				break
			}
			if c == ' ' || c == '\t' {
				if p.testPatternEnds(false) {
					break
				}
				p.l.advance()
				continue
			}
		}
		if c == '$' {
			c1 := p.l.peek(1)
			if c1 == '(' || c1 == '{' || isIdentStart(c1) || containsASCII("?@$*#-!_", c1) {
				flush()
				if exp := p.dollar(); exp != nil {
					parts = append(parts, exp)
				}
				start, startI = p.l.b, p.l.i
				continue
			}
		}
		if c == '"' {
			flush()
			parts = append(parts, p.doubleQuoted())
			start, startI = p.l.b, p.l.i
			continue
		}
		if c == '\'' {
			flush()
			parts = append(parts, p.leaf("raw_string", p.l.next("arg")))
			start, startI = p.l.b, p.l.i
			continue
		}
		if c == '(' {
			parens++
		} else if c == ')' && parens > 0 {
			parens--
		}
		p.l.advance()
	}
	flush()
	return parts
}
func (p *parseState) testPrimary(closer string) *Node {
	p.l.skipBlanks()
	if p.l.peek(0) == ']' && (closer == "]" || closer == "]]" && p.l.peek(1) == ']') {
		return nil
	}
	return p.word()
}
