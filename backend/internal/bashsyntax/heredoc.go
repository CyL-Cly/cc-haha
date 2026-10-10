package bashsyntax

import "sort"

func (p *parseState) heredocRedirect(t token, fd *Node) *Node {
	op := p.leaf(tokenText(t), t)
	p.l.skipBlanks()
	start := p.l.b
	quoted := false
	delim := []uint16{}
	c := p.l.peek(0)
	if c == '\'' || c == '"' {
		quoted = true
		p.l.advance()
		for p.l.i < len(p.l.src) && p.l.peek(0) != c {
			delim = append(delim, uint16(p.l.peek(0)))
			p.l.advance()
		}
		if p.l.i < len(p.l.src) {
			p.l.advance()
		}
	} else if c == '\\' {
		quoted = true
		p.l.advance()
		if p.l.i < len(p.l.src) && p.l.peek(0) != '\n' {
			delim = append(delim, uint16(p.l.peek(0)))
			p.l.advance()
		}
		for p.l.i < len(p.l.src) && isIdentChar(p.l.peek(0)) {
			delim = append(delim, uint16(p.l.peek(0)))
			p.l.advance()
		}
	} else {
		for p.l.i < len(p.l.src) && isHeredocDelimChar(p.l.peek(0)) {
			delim = append(delim, uint16(p.l.peek(0)))
			p.l.advance()
		}
	}
	begin := p.node("heredoc_start", start, p.l.b)
	p.l.heredocs = append(p.l.heredocs, &heredocPending{delim: delim, stripTabs: tokenText(t) == "<<-", quoted: quoted})
	kids := []*Node{op, begin}
	idx := op.StartIndex
	if fd != nil {
		kids = append([]*Node{fd}, kids...)
		idx = fd.StartIndex
	}
	for {
		p.l.skipBlanks()
		c = p.l.peek(0)
		if c == '\n' || c == -1 || p.l.i >= len(p.l.src) {
			break
		}
		if c == '>' || c == '<' || isDigit(c) {
			save := p.l.save()
			r := p.redirect(false)
			if r != nil && r.Type == "file_redirect" {
				kids = append(kids, r)
				continue
			}
			p.l.restore(save)
		}
		if c == '|' && p.l.peek(1) != '|' {
			p.l.advance()
			p.l.skipBlanks()
			commands := []*Node{}
			for {
				cmd := p.command()
				if cmd == nil {
					break
				}
				commands = append(commands, cmd)
				p.l.skipBlanks()
				if p.l.peek(0) == '|' && p.l.peek(1) != '|' {
					s := p.l.b
					p.l.advance()
					commands = append(commands, p.node("|", s, p.l.b))
					p.l.skipBlanks()
					continue
				}
				break
			}
			if len(commands) > 0 {
				kids = append(kids, p.node("pipeline", commands[0].StartIndex, commands[len(commands)-1].EndIndex, commands...))
			}
			continue
		}
		if c == '&' && p.l.peek(1) == '&' || c == '|' && p.l.peek(1) == '|' {
			p.l.advance()
			p.l.advance()
			p.l.skipBlanks()
			if rhs := p.command(); rhs != nil {
				kids = append(kids, rhs)
			}
			continue
		}
		if containsASCII("&;()", c) {
			s := p.l.b
			for p.l.i < len(p.l.src) && p.l.peek(0) != '\n' {
				p.l.advance()
			}
			kids = append(kids, p.node("ERROR", s, p.l.b))
			break
		}
		if word := p.word(); word != nil {
			kids = append(kids, word)
			continue
		}
		s := p.l.b
		for p.l.i < len(p.l.src) && p.l.peek(0) != '\n' {
			p.l.advance()
		}
		if p.l.b > s {
			kids = append(kids, p.node("ERROR", s, p.l.b))
		}
		break
	}
	return p.node("heredoc_redirect", idx, p.l.b, kids...)
}

func unitsStartWith(src, prefix []uint16) bool {
	if len(src) < len(prefix) {
		return false
	}
	for i, c := range prefix {
		if src[i] != c {
			return false
		}
	}
	return true
}
func (p *parseState) scanHeredocBodies() {
	for p.l.i < len(p.l.src) && p.l.src[p.l.i] != '\n' {
		p.l.advance()
	}
	if p.l.i < len(p.l.src) {
		p.l.advance()
	}
	for _, hd := range p.l.heredocs {
		hd.bodyStart = p.l.b
		for p.l.i < len(p.l.src) {
			lineB, check := p.l.b, p.l.i
			if hd.stripTabs {
				for check < len(p.l.src) && p.l.src[check] == '\t' {
					check++
				}
			}
			next := check + len(hd.delim)
			if unitsStartWith(p.l.src[check:], hd.delim) && (next >= len(p.l.src) || p.l.src[next] == '\n' || p.l.src[next] == '\r') {
				hd.bodyEnd = lineB
				for p.l.i < check {
					p.l.advance()
				}
				hd.endStart = p.l.b
				for range hd.delim {
					p.l.advance()
				}
				hd.endEnd = p.l.b
				if p.l.i < len(p.l.src) && p.l.src[p.l.i] == '\n' {
					p.l.advance()
				}
				return
			}
			for p.l.i < len(p.l.src) && p.l.src[p.l.i] != '\n' {
				p.l.advance()
			}
			if p.l.i < len(p.l.src) {
				p.l.advance()
			}
		}
		hd.bodyEnd = p.l.b
		hd.endStart = p.l.b
		hd.endEnd = p.l.b
	}
}
func (p *parseState) restoreByte(b int) {
	p.l.i = sort.Search(len(p.l.src), func(i int) bool { return p.l.byteTable[i] >= b })
	p.l.b = b
}
func (p *parseState) heredocBodyContent(start, end int) []*Node {
	save := p.l.save()
	p.restoreByte(start)
	out := []*Node{}
	contentStart := p.l.b
	saw := false
	for p.l.b < end {
		c := p.l.peek(0)
		if c == '\\' {
			next := p.l.peek(1)
			if next == '$' || next == '`' || next == '\\' {
				p.l.advance()
				p.l.advance()
				continue
			}
			p.l.advance()
			continue
		}
		if c == '$' || c == '`' {
			pre := p.l.b
			// The original heredoc scanner calls parseDollarLike for both
			// dollar and backtick characters. Preserve its exact tree shape;
			// the security walker separately rejects unquoted heredocs.
			exp := p.dollar()
			if exp != nil && (exp.Type == "simple_expansion" || exp.Type == "expansion" || exp.Type == "command_substitution" || exp.Type == "arithmetic_expansion") {
				if saw && pre > contentStart {
					out = append(out, p.node("heredoc_content", contentStart, pre))
				}
				out = append(out, exp)
				contentStart = p.l.b
				saw = true
			}
			continue
		}
		p.l.advance()
	}
	if saw {
		out = append(out, p.node("heredoc_content", contentStart, end))
	}
	p.l.restore(save)
	return out
}

func (p *parseState) finishHeredoc(command, redirect *Node, preRedirects, redirects []*Node) *Node {
	p.scanHeredocBodies()
	if len(p.l.heredocs) > 0 {
		hd := p.l.heredocs[0]
		p.l.heredocs = p.l.heredocs[1:]
		if len(redirect.Children) >= 2 {
			body := []*Node{}
			if !hd.quoted {
				body = p.heredocBodyContent(hd.bodyStart, hd.bodyEnd)
			}
			redirect.Children = append(redirect.Children, p.node("heredoc_body", hd.bodyStart, hd.bodyEnd, body...), p.node("heredoc_end", hd.endStart, hd.endEnd))
			redirect.EndIndex = hd.endEnd
			redirect.Text = p.l.slice(sort.Search(len(p.l.byteTable), func(i int) bool { return p.l.byteTable[i] >= redirect.StartIndex }), sort.Search(len(p.l.byteTable), func(i int) bool { return p.l.byteTable[i] >= hd.endEnd }))
		}
	}
	all := append(append(append([]*Node{}, preRedirects...), redirect), redirects...)
	start := command.StartIndex
	if len(preRedirects) > 0 {
		start = min(start, preRedirects[0].StartIndex)
	}
	return p.node("redirected_statement", start, redirect.EndIndex, append([]*Node{command}, all...)...)
}
