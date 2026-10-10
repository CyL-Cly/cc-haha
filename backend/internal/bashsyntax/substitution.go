package bashsyntax

func (p *parseState) processSubstitution() *Node {
	c := p.l.peek(0)
	if c != '<' && c != '>' || p.l.peek(1) != '(' {
		return nil
	}
	start := p.l.b
	p.l.advance()
	p.l.advance()
	open := p.node(string(rune(c))+"(", start, p.l.b)
	body := p.statements(")")
	p.l.skipBlanks()
	var close *Node
	if p.l.peek(0) == ')' {
		s := p.l.b
		p.l.advance()
		close = p.node(")", s, p.l.b)
	} else {
		close = p.node(")", p.l.b, p.l.b)
	}
	return p.node("process_substitution", start, close.EndIndex, append(append([]*Node{open}, body...), close)...)
}
func (p *parseState) dollar() *Node {
	c1, start := p.l.peek(1), p.l.b
	if c1 == '(' && p.l.peek(2) == '(' || c1 == '[' {
		prefix, closing := "$((", "))"
		count := 3
		if c1 == '[' {
			prefix, closing, count = "$[", "]", 2
		}
		for i := 0; i < count; i++ {
			p.l.advance()
		}
		open := p.node(prefix, start, p.l.b)
		expr := p.arithmeticList(closing, "var")
		p.l.skipBlanks()
		var close *Node
		matched := p.l.peek(0) == ']'
		if closing == "))" {
			matched = p.l.peek(0) == ')' && p.l.peek(1) == ')'
		}
		if matched {
			s := p.l.b
			for range closing {
				p.l.advance()
			}
			close = p.node(closing, s, p.l.b)
		} else {
			close = p.node(closing, p.l.b, p.l.b)
		}
		return p.node("arithmetic_expansion", start, close.EndIndex, append(append([]*Node{open}, expr...), close)...)
	}
	if c1 == '(' {
		p.l.advance()
		p.l.advance()
		open := p.node("$(", start, p.l.b)
		body := p.statements(")")
		p.l.skipBlanks()
		var close *Node
		if p.l.peek(0) == ')' {
			s := p.l.b
			p.l.advance()
			close = p.node(")", s, p.l.b)
		} else {
			close = p.node(")", p.l.b, p.l.b)
		}
		if len(body) == 1 && body[0].Type == "redirected_statement" && len(body[0].Children) == 1 && body[0].Children[0].Type == "file_redirect" {
			body = body[0].Children
		}
		return p.node("command_substitution", start, close.EndIndex, append(append([]*Node{open}, body...), close)...)
	}
	if c1 == '{' {
		p.l.advance()
		p.l.advance()
		open := p.node("${", start, p.l.b)
		body := p.expansionBody()
		var close *Node
		if p.l.peek(0) == '}' {
			s := p.l.b
			p.l.advance()
			close = p.node("}", s, p.l.b)
		} else {
			close = p.node("}", p.l.b, p.l.b)
		}
		return p.node("expansion", start, close.EndIndex, append(append([]*Node{open}, body...), close)...)
	}
	p.l.advance()
	dollar := p.node("$", start, p.l.b)
	nc := p.l.peek(0)
	kind := ""
	if nc == '_' && !isIdentChar(p.l.peek(1)) {
		kind = "special_variable_name"
	} else if isIdentStart(nc) || isDigit(nc) {
		kind = "variable_name"
	} else if containsASCII("?$@*#-!_", nc) {
		kind = "special_variable_name"
	}
	if kind == "" {
		return dollar
	}
	begin := p.l.b
	if isIdentStart(nc) && kind == "variable_name" {
		for isIdentChar(p.l.peek(0)) {
			p.l.advance()
		}
	} else {
		p.l.advance()
	}
	name := p.node(kind, begin, p.l.b)
	return p.node("simple_expansion", start, p.l.b, dollar, name)
}
func (p *parseState) backtick() *Node {
	start := p.l.b
	p.l.advance()
	open := p.node("`", start, p.l.b)
	p.inBacktick++
	body := []*Node{}
	for {
		p.l.skipBlanks()
		if p.l.peek(0) == '`' || p.l.peek(0) == -1 {
			break
		}
		save := p.l.save()
		t := p.l.next("cmd")
		if t.Type == "EOF" || t.Type == "BACKTICK" {
			p.l.restore(save)
			break
		}
		if t.Type == "NEWLINE" {
			continue
		}
		p.l.restore(save)
		stmt := p.andOr()
		if stmt == nil {
			break
		}
		body = append(body, stmt)
		p.l.skipBlanks()
		if p.l.peek(0) == '`' {
			break
		}
		save = p.l.save()
		sep := p.l.next("cmd")
		value := tokenText(sep)
		if sep.Type == "OP" && (value == ";" || value == "&") {
			body = append(body, p.leaf(value, sep))
		} else if sep.Type != "NEWLINE" {
			p.l.restore(save)
		}
	}
	p.inBacktick--
	var close *Node
	if p.l.peek(0) == '`' {
		s := p.l.b
		p.l.advance()
		close = p.node("`", s, p.l.b)
	} else {
		close = p.node("`", p.l.b, p.l.b)
	}
	if len(body) == 0 {
		return nil
	}
	return p.node("command_substitution", start, close.EndIndex, append(append([]*Node{open}, body...), close)...)
}
