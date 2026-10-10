package bashsyntax

func (p *parseState) assignment() *Node {
	save := p.l.save()
	p.l.skipBlanks()
	start := p.l.b
	if !isIdentStart(p.l.peek(0)) {
		p.l.restore(save)
		return nil
	}
	for isIdentChar(p.l.peek(0)) {
		p.l.advance()
	}
	nameEnd := p.l.b
	subEnd := nameEnd
	if p.l.peek(0) == '[' {
		p.l.advance()
		depth := 1
		for p.l.i < len(p.l.src) && depth > 0 {
			if p.l.peek(0) == '[' {
				depth++
			} else if p.l.peek(0) == ']' {
				depth--
			}
			p.l.advance()
		}
		subEnd = p.l.b
	}
	c, c1 := p.l.peek(0), p.l.peek(1)
	op := ""
	if c == '=' && c1 != '=' {
		op = "="
	} else if c == '+' && c1 == '=' {
		op = "+="
	} else {
		p.l.restore(save)
		return nil
	}
	lhs := p.node("variable_name", start, nameEnd)
	if subEnd > nameEnd {
		lhs = p.node("subscript", start, subEnd, lhs, p.node("[", nameEnd, nameEnd+1), p.legacySubscriptIndex(nameEnd+1, subEnd-1), p.node("]", subEnd-1, subEnd))
	}
	opStart := p.l.b
	p.l.advance()
	if op == "+=" {
		p.l.advance()
	}
	operator := p.node(op, opStart, p.l.b)
	var value *Node
	if p.l.peek(0) == '(' {
		open := p.leaf("(", p.l.next("cmd"))
		elems := []*Node{open}
		for {
			p.l.skipBlanks()
			if p.l.peek(0) == ')' {
				break
			}
			element := p.word()
			if element == nil {
				break
			}
			elems = append(elems, element)
		}
		closing := p.l.next("cmd")
		var close *Node
		if tokenText(closing) == ")" {
			close = p.leaf(")", closing)
		} else {
			close = p.node(")", open.EndIndex, open.EndIndex)
		}
		elems = append(elems, close)
		value = p.node("array", open.StartIndex, close.EndIndex, elems...)
	} else if next := p.l.peek(0); next != -1 && !containsASCII(" \t\n;&|)}", next) {
		value = p.word()
	}
	kids := []*Node{lhs, operator}
	end := operator.EndIndex
	if value != nil {
		kids = append(kids, value)
		end = value.EndIndex
	}
	return p.node("variable_assignment", start, end, kids...)
}

func (p *parseState) legacySubscriptIndex(start, end int) *Node {
	text := p.byteUnits(start, end)
	allDigits := len(text) > 0
	for _, c := range text {
		if !isDigit(int(c)) {
			allDigits = false
		}
	}
	if allDigits {
		return p.node("number", start, end)
	}
	if len(text) >= 2 && text[0] == '$' {
		identifier := isIdentStart(int(text[1]))
		for _, c := range text[2:] {
			if !isIdentChar(int(c)) {
				identifier = false
			}
		}
		kind := "variable_name"
		if len(text) == 2 && containsASCII("@*#?-$!_", int(text[1])) && !isIdentStart(int(text[1])) {
			identifier = true
			kind = "special_variable_name"
		}
		if identifier {
			return p.node("simple_expansion", start, end, p.node("$", start, start+1), p.node(kind, start+1, end))
		}
	}
	return p.node("word", start, end)
}

func (p *parseState) redirectLiteralStart() bool {
	c := p.l.peek(0)
	if c == -1 || c == '\n' || containsASCII("|&;()", c) {
		return false
	}
	if c == '<' || c == '>' {
		return p.l.peek(1) == '('
	}
	if isDigit(c) {
		j := p.l.i
		for j < len(p.l.src) && isDigit(int(p.l.src[j])) {
			j++
		}
		if j < len(p.l.src) && (p.l.src[j] == '>' || p.l.src[j] == '<') {
			return false
		}
	}
	return c != '}' && !(p.stopToken == "]" && c == ']')
}
func (p *parseState) redirect(greedy bool) *Node {
	save := p.l.save()
	p.l.skipBlanks()
	var fd *Node
	if isDigit(p.l.peek(0)) {
		start := p.l.b
		j := p.l.i
		for j < len(p.l.src) && isDigit(int(p.l.src[j])) {
			j++
		}
		if j < len(p.l.src) && (p.l.src[j] == '<' || p.l.src[j] == '>') {
			for p.l.i < j {
				p.l.advance()
			}
			fd = p.node("file_descriptor", start, p.l.b)
		}
	}
	t := p.l.next("arg")
	if t.Type != "OP" {
		p.l.restore(save)
		return nil
	}
	op := tokenText(t)
	if op == "<<" || op == "<<-" {
		return p.heredocRedirect(t, fd)
	}
	if op == "<<<" {
		operator := p.leaf(op, t)
		p.l.skipBlanks()
		target := p.word()
		kids := []*Node{}
		start := operator.StartIndex
		if fd != nil {
			kids = append(kids, fd)
			start = fd.StartIndex
		}
		kids = append(kids, operator)
		end := operator.EndIndex
		if target != nil {
			kids = append(kids, target)
			end = target.EndIndex
		}
		return p.node("herestring_redirect", start, end, kids...)
	}
	if op == "<&-" || op == ">&-" {
		operator := p.leaf(op, t)
		kids := []*Node{}
		start := operator.StartIndex
		if fd != nil {
			kids = append(kids, fd)
			start = fd.StartIndex
		}
		kids = append(kids, operator)
		p.l.skipBlanks()
		destSave := p.l.save()
		var target *Node
		if p.redirectLiteralStart() {
			target = p.word()
		}
		end := operator.EndIndex
		if target != nil {
			kids = append(kids, target)
			end = target.EndIndex
		} else {
			p.l.restore(destSave)
		}
		return p.node("file_redirect", start, end, kids...)
	}
	if op == ">" || op == ">>" || op == ">&" || op == ">|" || op == "&>" || op == "&>>" || op == "<" || op == "<&" {
		operator := p.leaf(op, t)
		kids := []*Node{}
		start := operator.StartIndex
		if fd != nil {
			kids = append(kids, fd)
			start = fd.StartIndex
		}
		kids = append(kids, operator)
		end, taken := operator.EndIndex, 0
		for {
			p.l.skipBlanks()
			if !p.redirectLiteralStart() || !greedy && taken >= 1 {
				break
			}
			var target *Node
			if (p.l.peek(0) == '<' || p.l.peek(0) == '>') && p.l.peek(1) == '(' {
				target = p.processSubstitution()
			} else {
				target = p.word()
			}
			if target == nil {
				break
			}
			kids = append(kids, target)
			end = target.EndIndex
			taken++
		}
		return p.node("file_redirect", start, end, kids...)
	}
	p.l.restore(save)
	return nil
}
