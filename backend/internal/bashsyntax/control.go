package bashsyntax

func (p *parseState) consumeKeyword(name string, kids []*Node) []*Node {
	p.skipNewlines()
	save := p.l.save()
	t := p.l.next("cmd")
	if t.Type == "WORD" && tokenText(t) == name {
		return append(kids, p.leaf(name, t))
	}
	p.l.restore(save)
	return kids
}
func (p *parseState) ifStatement(t token) *Node {
	kw := p.leaf("if", t)
	kids := append([]*Node{kw}, p.statements("")...)
	kids = p.consumeKeyword("then", kids)
	kids = append(kids, p.statements("")...)
	for {
		save := p.l.save()
		next := p.l.next("cmd")
		value := tokenText(next)
		if next.Type == "WORD" && value == "elif" {
			e := p.leaf("elif", next)
			branch := append([]*Node{e}, p.statements("")...)
			branch = p.consumeKeyword("then", branch)
			branch = append(branch, p.statements("")...)
			kids = append(kids, p.node("elif_clause", e.StartIndex, branch[len(branch)-1].EndIndex, branch...))
		} else if next.Type == "WORD" && value == "else" {
			e := p.leaf("else", next)
			body := p.statements("")
			branch := append([]*Node{e}, body...)
			kids = append(kids, p.node("else_clause", e.StartIndex, branch[len(branch)-1].EndIndex, branch...))
		} else {
			p.l.restore(save)
			break
		}
	}
	kids = p.consumeKeyword("fi", kids)
	return p.node("if_statement", kw.StartIndex, kids[len(kids)-1].EndIndex, kids...)
}
func (p *parseState) whileStatement(t token) *Node {
	kw := p.leaf(tokenText(t), t)
	kids := append([]*Node{kw}, p.statements("")...)
	if body := p.doGroup(); body != nil {
		kids = append(kids, body)
	}
	return p.node("while_statement", kw.StartIndex, kids[len(kids)-1].EndIndex, kids...)
}
func (p *parseState) forStatement(t token) *Node {
	kw := p.leaf(tokenText(t), t)
	p.l.skipBlanks()
	if tokenText(t) == "for" && p.l.peek(0) == '(' && p.l.peek(1) == '(' {
		return p.arithmeticFor(kw)
	}
	kids := []*Node{kw}
	variable := p.l.next("arg")
	kids = append(kids, p.node("variable_name", variable.Start, variable.End))
	p.l.skipBlanks()
	save := p.l.save()
	in := p.l.next("arg")
	if in.Type == "WORD" && tokenText(in) == "in" {
		kids = append(kids, p.leaf("in", in))
		for {
			p.l.skipBlanks()
			c := p.l.peek(0)
			if c == ';' || c == '\n' || c == -1 {
				break
			}
			word := p.word()
			if word == nil {
				break
			}
			kids = append(kids, word)
		}
	} else {
		p.l.restore(save)
	}
	save = p.l.save()
	sep := p.l.next("cmd")
	if sep.Type == "OP" && tokenText(sep) == ";" {
		kids = append(kids, p.leaf(";", sep))
	} else if sep.Type != "NEWLINE" {
		p.l.restore(save)
	}
	if body := p.doGroup(); body != nil {
		kids = append(kids, body)
	}
	return p.node("for_statement", kw.StartIndex, kids[len(kids)-1].EndIndex, kids...)
}

func (p *parseState) arithmeticFor(kw *Node) *Node {
	s := p.l.b
	p.l.advance()
	p.l.advance()
	kids := []*Node{kw, p.node("((", s, p.l.b)}
	for k := 0; k < 3; k++ {
		p.l.skipBlanks()
		stop := "))"
		if k < 2 {
			stop = ";"
		}
		kids = append(kids, p.arithmeticList(stop, "assign")...)
		if k < 2 && p.l.peek(0) == ';' {
			s = p.l.b
			p.l.advance()
			kids = append(kids, p.node(";", s, p.l.b))
		}
	}
	p.l.skipBlanks()
	if p.l.peek(0) == ')' && p.l.peek(1) == ')' {
		s = p.l.b
		p.l.advance()
		p.l.advance()
		kids = append(kids, p.node("))", s, p.l.b))
	}
	save := p.l.save()
	sep := p.l.next("cmd")
	if sep.Type == "OP" && tokenText(sep) == ";" {
		kids = append(kids, p.leaf(";", sep))
	} else if sep.Type != "NEWLINE" {
		p.l.restore(save)
	}
	if body := p.doGroup(); body != nil {
		kids = append(kids, body)
	} else {
		p.skipNewlines()
		p.l.skipBlanks()
		if p.l.peek(0) == '{' {
			s = p.l.b
			p.l.advance()
			open := p.node("{", s, p.l.b)
			body := p.statements("}")
			s = p.l.b
			if p.l.peek(0) == '}' {
				p.l.advance()
			}
			close := p.node("}", s, p.l.b)
			kids = append(kids, p.node("compound_statement", open.StartIndex, close.EndIndex, append(append([]*Node{open}, body...), close)...))
		}
	}
	return p.node("c_style_for_statement", kw.StartIndex, kids[len(kids)-1].EndIndex, kids...)
}

func (p *parseState) unset(t token) *Node {
	kw := p.leaf("unset", t)
	kids := []*Node{kw}
	for {
		p.l.skipBlanks()
		c := p.l.peek(0)
		if c == -1 || containsASCII("\n;&|)<>", c) {
			break
		}
		arg := p.word()
		if arg == nil {
			break
		}
		if arg.Type == "word" {
			units := p.byteUnits(arg.StartIndex, arg.EndIndex)
			if len(units) == 0 || units[0] != '-' {
				arg = p.node("variable_name", arg.StartIndex, arg.EndIndex)
			}
		}
		kids = append(kids, arg)
	}
	return p.node("unset_command", kw.StartIndex, kids[len(kids)-1].EndIndex, kids...)
}
func (p *parseState) doGroup() *Node {
	p.skipNewlines()
	save := p.l.save()
	t := p.l.next("cmd")
	if t.Type != "WORD" || tokenText(t) != "do" {
		p.l.restore(save)
		return nil
	}
	kw := p.leaf("do", t)
	kids := append([]*Node{kw}, p.statements("")...)
	kids = p.consumeKeyword("done", kids)
	return p.node("do_group", kw.StartIndex, kids[len(kids)-1].EndIndex, kids...)
}
func (p *parseState) functionStatement(t token) *Node {
	kw := p.leaf("function", t)
	p.l.skipBlanks()
	nameToken := p.l.next("arg")
	name := p.node("word", nameToken.Start, nameToken.End)
	kids := []*Node{kw, name}
	p.l.skipBlanks()
	if p.l.peek(0) == '(' && p.l.peek(1) == ')' {
		kids = append(kids, p.leaf("(", p.l.next("cmd")), p.leaf(")", p.l.next("cmd")))
	}
	p.l.skipBlanks()
	p.skipNewlines()
	if body := p.command(); body != nil {
		if body.Type == "redirected_statement" && len(body.Children) >= 2 && body.Children[0].Type == "compound_statement" {
			kids = append(kids, body.Children...)
		} else {
			kids = append(kids, body)
		}
	}
	return p.node("function_definition", kw.StartIndex, kids[len(kids)-1].EndIndex, kids...)
}
func (p *parseState) declaration(t token) *Node {
	kw := p.leaf(tokenText(t), t)
	kids := []*Node{kw}
	for {
		p.l.skipBlanks()
		c := p.l.peek(0)
		if c == -1 || containsASCII("\n;&|)<>", c) {
			break
		}
		if assignment := p.assignment(); assignment != nil {
			kids = append(kids, assignment)
			continue
		}
		if c == '"' || c == '\'' || c == '$' {
			if word := p.word(); word != nil {
				kids = append(kids, word)
				continue
			}
			break
		}
		save := p.l.save()
		next := p.l.next("arg")
		value := tokenText(next)
		if next.Type == "WORD" || next.Type == "NUMBER" {
			kind := "word"
			if value != "" && value[0] != '-' && isIdentStart(int(value[0])) {
				kind = "variable_name"
			}
			kids = append(kids, p.leaf(kind, next))
		} else {
			p.l.restore(save)
			break
		}
	}
	return p.node("declaration_command", kw.StartIndex, kids[len(kids)-1].EndIndex, kids...)
}
