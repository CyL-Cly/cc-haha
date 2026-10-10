package bashsyntax

var arithmeticPrecedence = map[string]int{"=": 2, "+=": 2, "-=": 2, "*=": 2, "/=": 2, "%=": 2, "<<=": 2, ">>=": 2, "&=": 2, "^=": 2, "|=": 2, "||": 4, "&&": 5, "|": 6, "^": 7, "&": 8, "==": 9, "!=": 9, "<": 10, ">": 10, "<=": 10, ">=": 10, "<<": 11, ">>": 11, "+": 12, "-": 12, "*": 13, "/": 13, "%": 13, "**": 14}
var arithmeticRight = map[string]bool{"=": true, "+=": true, "-=": true, "*=": true, "/=": true, "%=": true, "<<=": true, ">>=": true, "&=": true, "^=": true, "|=": true, "**": true}

func (p *parseState) arithmeticStop(stop string) bool {
	c := p.l.peek(0)
	switch stop {
	case "))":
		return c == ')' && p.l.peek(1) == ')'
	case ")":
		return c == ')'
	case ";":
		return c == ';'
	case ":":
		return c == ':'
	case "]":
		return c == ']'
	case "}":
		return c == '}'
	case ":}":
		return c == ':' || c == '}'
	}
	return c == -1 || c == '\n'
}
func (p *parseState) arithmeticList(stop, mode string) []*Node {
	out := []*Node{}
	for {
		if node := p.arithmeticTernary(stop, mode); node != nil {
			out = append(out, node)
		}
		p.l.skipBlanks()
		if p.l.peek(0) == ',' && !p.arithmeticStop(stop) {
			p.l.advance()
			continue
		}
		break
	}
	return out
}
func (p *parseState) arithmeticTernary(stop, mode string) *Node {
	cond := p.arithmeticBinary(stop, 0, mode)
	if cond == nil {
		return nil
	}
	p.l.skipBlanks()
	if p.l.peek(0) != '?' {
		return cond
	}
	s := p.l.b
	p.l.advance()
	q := p.node("?", s, p.l.b)
	yes := p.arithmeticBinary(":", 0, mode)
	p.l.skipBlanks()
	var colon *Node
	if p.l.peek(0) == ':' {
		s = p.l.b
		p.l.advance()
		colon = p.node(":", s, p.l.b)
	} else {
		colon = p.node(":", p.l.b, p.l.b)
	}
	no := p.arithmeticTernary(stop, mode)
	kids := []*Node{cond, q}
	if yes != nil {
		kids = append(kids, yes)
	}
	kids = append(kids, colon)
	end := colon.EndIndex
	if no != nil {
		kids = append(kids, no)
		end = no.EndIndex
	}
	return p.node("ternary_expression", cond.StartIndex, end, kids...)
}
func (p *parseState) scanArithmeticOp() string {
	for _, op := range []string{"<<=", ">>=", "**", "<<", ">>", "==", "!=", "<=", ">=", "&&", "||", "+=", "-=", "*=", "/=", "%=", "&=", "^=", "|=", "+", "-", "*", "/", "%", "<", ">", "&", "|", "^", "="} {
		match := true
		for i, c := range op {
			if p.l.peek(i) != int(c) {
				match = false
				break
			}
		}
		if match {
			if (op == "+" || op == "-") && p.l.peek(1) == p.l.peek(0) {
				continue
			}
			return op
		}
	}
	return ""
}
func (p *parseState) arithmeticBinary(stop string, min int, mode string) *Node {
	left := p.arithmeticUnary(stop, mode)
	if left == nil {
		return nil
	}
	for {
		p.l.skipBlanks()
		if p.arithmeticStop(stop) || p.l.peek(0) == ',' {
			break
		}
		op := p.scanArithmeticOp()
		prec, ok := arithmeticPrecedence[op]
		if !ok || prec < min {
			break
		}
		s := p.l.b
		for range op {
			p.l.advance()
		}
		operator := p.node(op, s, p.l.b)
		nextMin := prec + 1
		if arithmeticRight[op] {
			nextMin = prec
		}
		right := p.arithmeticBinary(stop, nextMin, mode)
		if right == nil {
			break
		}
		left = p.node("binary_expression", left.StartIndex, right.EndIndex, left, operator, right)
	}
	return left
}
func (p *parseState) arithmeticUnary(stop, mode string) *Node {
	p.l.skipBlanks()
	if p.arithmeticStop(stop) {
		return nil
	}
	c, c1 := p.l.peek(0), p.l.peek(1)
	if (c == '+' && c1 == '+') || (c == '-' && c1 == '-') {
		s := p.l.b
		p.l.advance()
		p.l.advance()
		op := p.node(string([]rune{rune(c), rune(c1)}), s, p.l.b)
		inner := p.arithmeticUnary(stop, mode)
		if inner == nil {
			return op
		}
		return p.node("unary_expression", op.StartIndex, inner.EndIndex, op, inner)
	}
	if containsASCII("-+!~", c) {
		s := p.l.b
		if mode != "var" && c == '-' && isDigit(c1) {
			p.l.advance()
			for isDigit(p.l.peek(0)) {
				p.l.advance()
			}
			return p.node("number", s, p.l.b)
		}
		p.l.advance()
		op := p.node(string(rune(c)), s, p.l.b)
		inner := p.arithmeticUnary(stop, mode)
		if inner == nil {
			return op
		}
		return p.node("unary_expression", op.StartIndex, inner.EndIndex, op, inner)
	}
	primary := p.arithmeticPrimary(stop, mode)
	if primary == nil {
		return nil
	}
	c, c1 = p.l.peek(0), p.l.peek(1)
	if (c == '+' && c1 == '+') || (c == '-' && c1 == '-') {
		s := p.l.b
		p.l.advance()
		p.l.advance()
		op := p.node(string([]rune{rune(c), rune(c1)}), s, p.l.b)
		return p.node("postfix_expression", primary.StartIndex, op.EndIndex, primary, op)
	}
	return primary
}
func (p *parseState) arithmeticPrimary(stop, mode string) *Node {
	p.l.skipBlanks()
	if p.arithmeticStop(stop) {
		return nil
	}
	c := p.l.peek(0)
	if c == '(' {
		s := p.l.b
		p.l.advance()
		open := p.node("(", s, p.l.b)
		inner := p.arithmeticList(")", mode)
		p.l.skipBlanks()
		var close *Node
		if p.l.peek(0) == ')' {
			s = p.l.b
			p.l.advance()
			close = p.node(")", s, p.l.b)
		} else {
			close = p.node(")", p.l.b, p.l.b)
		}
		return p.node("parenthesized_expression", open.StartIndex, close.EndIndex, append(append([]*Node{open}, inner...), close)...)
	}
	if c == '"' {
		return p.doubleQuoted()
	}
	if c == '$' {
		return p.dollar()
	}
	if isDigit(c) {
		s := p.l.b
		for isDigit(p.l.peek(0)) {
			p.l.advance()
		}
		if p.l.b-s == 1 && c == '0' && (p.l.peek(0) == 'x' || p.l.peek(0) == 'X') {
			p.l.advance()
			for isHexDigit(p.l.peek(0)) {
				p.l.advance()
			}
		} else if p.l.peek(0) == '#' {
			p.l.advance()
			for isBaseDigit(p.l.peek(0)) {
				p.l.advance()
			}
		}
		return p.node("number", s, p.l.b)
	}
	if isIdentStart(c) {
		s := p.l.b
		for isIdentChar(p.l.peek(0)) {
			p.l.advance()
		}
		next := p.l.peek(0)
		if mode == "assign" {
			p.l.skipBlanks()
			if p.l.peek(0) == '=' && p.l.peek(1) != '=' {
				name := p.node("variable_name", s, p.l.b)
				opStart := p.l.b
				p.l.advance()
				eq := p.node("=", opStart, p.l.b)
				value := p.arithmeticTernary(stop, mode)
				kids := []*Node{name, eq}
				end := eq.EndIndex
				if value != nil {
					kids = append(kids, value)
					end = value.EndIndex
				}
				return p.node("variable_assignment", s, end, kids...)
			}
		}
		if next == '[' {
			name := p.node("variable_name", s, p.l.b)
			begin := p.l.b
			p.l.advance()
			open := p.node("[", begin, p.l.b)
			index := p.arithmeticTernary("]", "var")
			if index == nil {
				index = p.dollar()
			}
			p.l.skipBlanks()
			var close *Node
			if p.l.peek(0) == ']' {
				begin = p.l.b
				p.l.advance()
				close = p.node("]", begin, p.l.b)
			} else {
				close = p.node("]", p.l.b, p.l.b)
			}
			kids := []*Node{name, open}
			if index != nil {
				kids = append(kids, index)
			}
			kids = append(kids, close)
			return p.node("subscript", s, close.EndIndex, kids...)
		}
		kind := "word"
		if mode == "var" {
			kind = "variable_name"
		}
		return p.node(kind, s, p.l.b)
	}
	return nil
}
func (p *parseState) subscriptIndex() *Node {
	p.l.skipBlanks()
	c := p.l.peek(0)
	if (c == '@' || c == '*') && p.l.peek(1) == ']' {
		s := p.l.b
		p.l.advance()
		return p.node("word", s, p.l.b)
	}
	if c == '(' && p.l.peek(1) == '(' {
		s := p.l.b
		p.l.advance()
		p.l.advance()
		open := p.node("((", s, p.l.b)
		inner := p.arithmeticTernary("))", "var")
		p.l.skipBlanks()
		var close *Node
		if p.l.peek(0) == ')' && p.l.peek(1) == ')' {
			s = p.l.b
			p.l.advance()
			p.l.advance()
			close = p.node("))", s, p.l.b)
		} else {
			close = p.node("))", p.l.b, p.l.b)
		}
		kids := []*Node{open}
		if inner != nil {
			kids = append(kids, inner)
		}
		kids = append(kids, close)
		return p.node("compound_statement", open.StartIndex, close.EndIndex, kids...)
	}
	return p.arithmeticTernary("]", "word")
}
