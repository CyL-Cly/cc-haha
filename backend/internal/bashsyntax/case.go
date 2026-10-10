package bashsyntax

func (p *parseState) caseStatement(t token) *Node {
	kw := p.leaf("case", t)
	kids := []*Node{kw}
	p.l.skipBlanks()
	if word := p.word(); word != nil {
		kids = append(kids, word)
	}
	p.l.skipBlanks()
	kids = p.consumeKeyword("in", kids)
	p.skipNewlines()
	for {
		p.l.skipBlanks()
		p.skipNewlines()
		save := p.l.save()
		next := p.l.next("arg")
		if next.Type == "WORD" && tokenText(next) == "esac" {
			kids = append(kids, p.leaf("esac", next))
			break
		}
		if next.Type == "EOF" {
			break
		}
		p.l.restore(save)
		item := p.caseItem()
		if item == nil {
			break
		}
		kids = append(kids, item)
	}
	return p.node("case_statement", kw.StartIndex, kids[len(kids)-1].EndIndex, kids...)
}

func (p *parseState) caseItem() *Node {
	p.l.skipBlanks()
	start := p.l.b
	kids := []*Node{}
	if p.l.peek(0) == '(' {
		s := p.l.b
		p.l.advance()
		kids = append(kids, p.node("(", s, p.l.b))
	}
	first := true
	for {
		p.l.skipBlanks()
		c := p.l.peek(0)
		if c == ')' || c == -1 {
			break
		}
		patterns := p.casePattern()
		if len(patterns) == 0 {
			break
		}
		if !first && len(patterns) > 1 {
			for i, part := range patterns {
				if part.Type == "extglob_pattern" {
					patterns[i] = p.node("word", part.StartIndex, part.EndIndex)
				}
			}
			kids = append(kids, p.node("concatenation", patterns[0].StartIndex, patterns[len(patterns)-1].EndIndex, patterns...))
		} else {
			kids = append(kids, patterns...)
		}
		first = false
		p.l.skipBlanks()
		if p.l.peek(0) == '\\' && p.l.peek(1) == '\n' {
			p.l.advance()
			p.l.advance()
			p.l.skipBlanks()
		}
		if p.l.peek(0) != '|' {
			break
		}
		s := p.l.b
		p.l.advance()
		kids = append(kids, p.node("|", s, p.l.b))
		if p.l.peek(0) == '\\' && p.l.peek(1) == '\n' {
			p.l.advance()
			p.l.advance()
		}
	}
	if p.l.peek(0) == ')' {
		s := p.l.b
		p.l.advance()
		kids = append(kids, p.node(")", s, p.l.b))
	}
	body := p.statements("")
	kids = append(kids, body...)
	save := p.l.save()
	term := p.l.next("cmd")
	value := tokenText(term)
	if term.Type == "OP" && (value == ";;" || value == ";&" || value == ";;&") {
		kids = append(kids, p.leaf(value, term))
	} else {
		p.l.restore(save)
	}
	if len(kids) == 0 {
		return nil
	}
	if len(body) == 0 {
		for i, node := range kids {
			if node.Type != "extglob_pattern" {
				continue
			}
			u := p.byteUnits(node.StartIndex, node.EndIndex)
			if len(u) >= 2 && containsASCII("-+?*@!", int(u[0])) && isLetter(int(u[1])) && !unitsContainAny(u, "*?(") {
				kids[i] = p.node("word", node.StartIndex, node.EndIndex)
			}
		}
	}
	return p.node("case_item", start, kids[len(kids)-1].EndIndex, kids...)
}

func unitsContainAny(units []uint16, chars string) bool {
	for _, u := range units {
		if containsASCII(chars, int(u)) {
			return true
		}
	}
	return false
}
func isLetter(c int) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func hasExtglobParen(units []uint16) bool {
	for i := 0; i+1 < len(units); i++ {
		if containsASCII("*?+@!", int(units[i])) && units[i+1] == '(' {
			return true
		}
	}
	return false
}

func (p *parseState) casePattern() []*Node {
	p.l.skipBlanks()
	save := p.l.save()
	start, startI := p.l.b, p.l.i
	depth := 0
	hasDollar, hasBracket, hasQuote := false, false, false
	for p.l.i < len(p.l.src) {
		c := p.l.peek(0)
		if c == '\\' && p.l.i+1 < len(p.l.src) {
			p.l.advance()
			p.l.advance()
			continue
		}
		if c == '"' || c == '\'' {
			hasQuote = true
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
		if c == '(' {
			depth++
			p.l.advance()
			continue
		}
		if depth > 0 {
			if c == ')' {
				depth--
				p.l.advance()
				continue
			}
			if c == '\n' {
				break
			}
			p.l.advance()
			continue
		}
		if containsASCII(")| \t\n", c) {
			break
		}
		if c == '$' {
			hasDollar = true
		}
		if c == '[' {
			hasBracket = true
		}
		p.l.advance()
	}
	if p.l.b == start {
		return nil
	}
	u := p.l.src[startI:p.l.i]
	extglob := hasExtglobParen(u)
	if hasQuote && !extglob {
		p.l.restore(save)
		return p.casePatternSegmented()
	}
	if !extglob && (hasDollar || hasBracket) {
		p.l.restore(save)
		word := p.word()
		if word != nil {
			return []*Node{word}
		}
		return nil
	}
	kind := "word"
	if extglob || unitsContainAny(u, "*?") || len(u) >= 2 && containsASCII("-+?*@!", int(u[0])) && isLetter(int(u[1])) {
		kind = "extglob_pattern"
	}
	return []*Node{p.node(kind, start, p.l.b)}
}

func (p *parseState) casePatternSegmented() []*Node {
	parts := []*Node{}
	start, startI := p.l.b, p.l.i
	flush := func() {
		if p.l.i > startI {
			kind := "word"
			if unitsContainAny(p.l.src[startI:p.l.i], "*?") {
				kind = "extglob_pattern"
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
		if containsASCII(")| \t\n", c) {
			break
		}
		p.l.advance()
	}
	flush()
	return parts
}
