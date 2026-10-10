package bashsyntax

func digitsOnly(u []uint16) bool {
	if len(u) == 0 {
		return false
	}
	for _, c := range u {
		if !isDigit(int(c)) {
			return false
		}
	}
	return true
}
func numberUnits(u []uint16) bool {
	if len(u) > 0 && u[0] == '-' {
		u = u[1:]
	}
	return digitsOnly(u)
}
func (p *parseState) braceExpression() *Node {
	save := p.l.save()
	if p.l.peek(0) != '{' {
		return nil
	}
	o := p.l.b
	p.l.advance()
	oe := p.l.b
	s1 := p.l.b
	for isDigit(p.l.peek(0)) || isIdentStart(p.l.peek(0)) {
		p.l.advance()
	}
	e1 := p.l.b
	if s1 == e1 || p.l.peek(0) != '.' || p.l.peek(1) != '.' {
		p.l.restore(save)
		return nil
	}
	d := p.l.b
	p.l.advance()
	p.l.advance()
	de := p.l.b
	s2 := p.l.b
	for isDigit(p.l.peek(0)) || isIdentStart(p.l.peek(0)) {
		p.l.advance()
	}
	e2 := p.l.b
	if s2 == e2 || p.l.peek(0) != '}' {
		p.l.restore(save)
		return nil
	}
	c := p.l.b
	p.l.advance()
	ce := p.l.b
	u1, u2 := p.byteUnits(s1, e1), p.byteUnits(s2, e2)
	n1, n2 := digitsOnly(u1), digitsOnly(u2)
	if n1 != n2 || !n1 && (len(u1) != 1 || len(u2) != 1) {
		p.l.restore(save)
		return nil
	}
	kind := "word"
	if n1 {
		kind = "number"
	}
	return p.node("brace_expression", o, ce, p.node("{", o, oe), p.node(kind, s1, e1), p.node("..", d, de), p.node(kind, s2, e2), p.node("}", c, ce))
}
func (p *parseState) braceConcatenation() []*Node {
	if p.l.peek(0) != '{' {
		return nil
	}
	s := p.l.b
	p.l.advance()
	parts := []*Node{p.node("word", s, p.l.b)}
	for p.l.i < len(p.l.src) {
		c := p.l.peek(0)
		if containsASCII("}\n;|& \t<>()", c) {
			break
		}
		if c == '[' || c == ']' {
			s = p.l.b
			p.l.advance()
			parts = append(parts, p.node("word", s, p.l.b))
			continue
		}
		s = p.l.b
		for p.l.i < len(p.l.src) && !containsASCII("}\n;|& \t<>()[]", p.l.peek(0)) {
			p.l.advance()
		}
		if p.l.b == s {
			break
		}
		kind := "word"
		if numberUnits(p.byteUnits(s, p.l.b)) {
			kind = "number"
		}
		parts = append(parts, p.node(kind, s, p.l.b))
	}
	if p.l.peek(0) == '}' {
		s = p.l.b
		p.l.advance()
		parts = append(parts, p.node("word", s, p.l.b))
	}
	return parts
}
func arithmeticBasePrefix(u []uint16) bool {
	if len(u) > 0 && u[0] == '-' {
		u = u[1:]
	}
	if len(u) > 2 && u[0] == '0' && u[1] == 'x' {
		u = u[2:]
	}
	return len(u) > 1 && u[len(u)-1] == '#' && digitsOnly(u[:len(u)-1])
}
