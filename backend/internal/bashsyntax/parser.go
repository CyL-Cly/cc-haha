package bashsyntax

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var ErrParseAborted = errors.New("native Bash syntax parser aborted")

type UnsupportedSyntax struct{ Kind string }

func (e *UnsupportedSyntax) Error() string { return "native Bash syntax not ported: " + e.Kind }

type ParseOptions struct {
	Timeout         time.Duration
	DisableDeadline bool
}
type parseState struct {
	l            *lexer
	ctx          context.Context
	bytes, nodes int
	deadline     time.Time
	noDeadline   bool
	inBacktick   int
	stopToken    string
}
type lexSave struct{ i, b int }

func (l *lexer) save() lexSave     { return lexSave{l.i, l.b} }
func (l *lexer) restore(s lexSave) { l.i, l.b = s.i, s.b }
func tokenText(t token) string     { var s string; _ = json.Unmarshal(t.Value, &s); return s }

// Parse retains the original parser's allocation and deadline budgets. This
// leaf is not bound to the Harness until its full grammar/security consumer is
// ported; an unfinished grammar branch returns a capability, never a fake AST.
func Parse(ctx context.Context, input json.RawMessage, options ParseOptions) (result *Node, err error) {
	if ctx == nil {
		return nil, errors.New("Bash parser requires actual context")
	}
	l, err := newLexer(input)
	if err != nil {
		return nil, err
	}
	bytes := l.byteAt(len(l.src))
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 50 * time.Millisecond
	}
	p := &parseState{l: l, ctx: ctx, bytes: bytes, deadline: time.Now().Add(timeout), noDeadline: options.DisableDeadline}
	defer func() {
		if failure := recover(); failure != nil {
			if value, ok := failure.(error); ok {
				result = nil
				err = value
			} else {
				panic(failure)
			}
		}
	}()
	return p.program(), nil
}
func (p *parseState) node(kind string, start, end int, children ...*Node) *Node {
	p.nodes++
	if p.nodes > 50000 {
		panic(ErrParseAborted)
	}
	if p.nodes&0x7f == 0 {
		if err := p.ctx.Err(); err != nil {
			panic(err)
		}
		if !p.noDeadline && time.Now().After(p.deadline) {
			panic(ErrParseAborted)
		}
	}
	if children == nil {
		children = []*Node{}
	}
	table := p.l.byteTable
	sc := sort.Search(len(table), func(i int) bool { return table[i] >= start })
	ec := sort.Search(len(table), func(i int) bool { return table[i] >= end })
	return &Node{kind, p.l.slice(sc, ec), start, end, children}
}
func (p *parseState) byteUnits(start, end int) []uint16 {
	table := p.l.byteTable
	sc := sort.Search(len(table), func(i int) bool { return table[i] >= start })
	ec := sort.Search(len(table), func(i int) bool { return table[i] >= end })
	return p.l.src[min(sc, len(p.l.src)):min(ec, len(p.l.src))]
}
func (p *parseState) leaf(kind string, t token) *Node { return p.node(kind, t.Start, t.End) }
func (p *parseState) unsupported(kind string)         { panic(&UnsupportedSyntax{kind}) }
func (p *parseState) program() *Node {
	kids := []*Node{}
	p.l.skipBlanks()
	for {
		save := p.l.save()
		t := p.l.next("cmd")
		if t.Type == "NEWLINE" {
			p.l.skipBlanks()
			continue
		}
		p.l.restore(save)
		break
	}
	start := p.l.b
	for p.l.i < len(p.l.src) {
		save := p.l.save()
		t := p.l.next("cmd")
		if t.Type == "EOF" {
			break
		}
		if t.Type == "NEWLINE" {
			continue
		}
		if t.Type == "COMMENT" {
			kids = append(kids, p.leaf("comment", t))
			continue
		}
		p.l.restore(save)
		statements := p.statements("")
		kids = append(kids, statements...)
		if len(statements) == 0 {
			t = p.l.next("cmd")
			if t.Type == "EOF" {
				break
			}
			if t.Type == "OP" && tokenText(t) == ";;" && len(kids) > 0 {
				continue
			}
			kids = append(kids, p.node("ERROR", t.Start, t.End))
		}
	}
	end := start
	if len(kids) > 0 {
		end = p.bytes
	}
	return p.node("program", start, end, kids...)
}
func terminator(t token, exact string) bool {
	value := tokenText(t)
	if t.Type == "OP" && (value == exact && exact != "" || value == ")" || value == "}" || value == ";;" || value == ";&" || value == ";;&" || value == "))" || value == "]]" || value == "]") {
		return true
	}
	return t.Type == "WORD" && (value == "then" || value == "elif" || value == "else" || value == "fi" || value == "do" || value == "done" || value == "esac")
}
func (p *parseState) statements(close string) []*Node {
	out := []*Node{}
	for {
		p.l.skipBlanks()
		save := p.l.save()
		t := p.l.next("cmd")
		if t.Type == "EOF" {
			p.l.restore(save)
			break
		}
		if t.Type == "NEWLINE" {
			if len(p.l.heredocs) > 0 {
				p.scanHeredocBodies()
			}
			continue
		}
		if t.Type == "COMMENT" {
			out = append(out, p.leaf("comment", t))
			continue
		}
		if terminator(t, close) || t.Type == "BACKTICK" && p.inBacktick > 0 {
			p.l.restore(save)
			break
		}
		p.l.restore(save)
		stmt := p.andOr()
		if stmt == nil {
			break
		}
		out = append(out, stmt)
		p.l.skipBlanks()
		save = p.l.save()
		sep := p.l.next("cmd")
		value := tokenText(sep)
		if sep.Type == "OP" && (value == ";" || value == "&") {
			out = append(out, p.leaf(value, sep))
			continue
		}
		if sep.Type == "NEWLINE" {
			if len(p.l.heredocs) > 0 {
				p.scanHeredocBodies()
			}
			continue
		}
		p.l.restore(save)
	}
	return out
}
func (p *parseState) skipNewlines() {
	for {
		save := p.l.save()
		t := p.l.next("cmd")
		if t.Type != "NEWLINE" {
			p.l.restore(save)
			break
		}
	}
}
func (p *parseState) andOr() *Node {
	left := p.pipeline()
	if left == nil {
		return nil
	}
	for {
		save := p.l.save()
		t := p.l.next("cmd")
		value := tokenText(t)
		if t.Type != "OP" || value != "&&" && value != "||" {
			p.l.restore(save)
			break
		}
		op := p.leaf(value, t)
		p.skipNewlines()
		right := p.pipeline()
		if right == nil {
			left = p.node("list", left.StartIndex, op.EndIndex, left, op)
			break
		}
		if right.Type == "redirected_statement" && len(right.Children) >= 2 {
			inner := right.Children[0]
			redirs := right.Children[1:]
			list := p.node("list", left.StartIndex, inner.EndIndex, left, op, inner)
			left = p.node("redirected_statement", list.StartIndex, redirs[len(redirs)-1].EndIndex, append([]*Node{list}, redirs...)...)
		} else {
			left = p.node("list", left.StartIndex, right.EndIndex, left, op, right)
		}
	}
	return left
}
func (p *parseState) pipeline() *Node {
	first := p.command()
	if first == nil {
		return nil
	}
	parts := []*Node{first}
	for {
		save := p.l.save()
		t := p.l.next("cmd")
		value := tokenText(t)
		if t.Type != "OP" || value != "|" && value != "|&" {
			p.l.restore(save)
			break
		}
		op := p.leaf(value, t)
		p.skipNewlines()
		next := p.command()
		if next == nil {
			parts = append(parts, op)
			break
		}
		if next.Type == "redirected_statement" && len(next.Children) >= 2 {
			inner := next.Children[0]
			redirs := next.Children[1:]
			parts = append(parts, op, inner)
			pipe := p.node("pipeline", parts[0].StartIndex, inner.EndIndex, parts...)
			parts = []*Node{p.node("redirected_statement", pipe.StartIndex, redirs[len(redirs)-1].EndIndex, append([]*Node{pipe}, redirs...)...)}
			continue
		}
		parts = append(parts, op, next)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return p.node("pipeline", parts[0].StartIndex, parts[len(parts)-1].EndIndex, parts...)
}
func (p *parseState) command() *Node {
	p.l.skipBlanks()
	save := p.l.save()
	t := p.l.next("cmd")
	value := tokenText(t)
	if t.Type == "EOF" {
		p.l.restore(save)
		return nil
	}
	if t.Type == "OP" && value == "!" {
		bang := p.leaf("!", t)
		inner := p.command()
		if inner == nil {
			p.l.restore(save)
			return nil
		}
		if inner.Type == "redirected_statement" && len(inner.Children) >= 2 {
			cmd := inner.Children[0]
			neg := p.node("negated_command", bang.StartIndex, cmd.EndIndex, bang, cmd)
			redirs := inner.Children[1:]
			return p.node("redirected_statement", neg.StartIndex, redirs[len(redirs)-1].EndIndex, append([]*Node{neg}, redirs...)...)
		}
		return p.node("negated_command", bang.StartIndex, inner.EndIndex, bang, inner)
	}
	if t.Type == "OP" && (value == "(" || value == "{") {
		open := p.leaf(value, t)
		close := ")"
		kind := "subshell"
		if value == "{" {
			close = "}"
			kind = "compound_statement"
		}
		body := p.statements(close)
		closing := p.l.next("cmd")
		var end *Node
		if closing.Type == "OP" && tokenText(closing) == close {
			end = p.leaf(close, closing)
		} else {
			end = p.node(close, open.EndIndex, open.EndIndex)
		}
		node := p.node(kind, open.StartIndex, end.EndIndex, append(append([]*Node{open}, body...), end)...)
		return p.maybeRedirect(node)
	}
	if t.Type == "OP" && value == "((" {
		open := p.leaf("((", t)
		expr := p.arithmeticList("))", "var")
		closing := p.l.next("cmd")
		var close *Node
		if tokenText(closing) == "))" {
			close = p.leaf("))", closing)
		} else {
			close = p.node("))", open.EndIndex, open.EndIndex)
		}
		return p.node("compound_statement", open.StartIndex, close.EndIndex, append(append([]*Node{open}, expr...), close)...)
	}
	if t.Type == "OP" && (value == "[" || value == "[[") {
		return p.testCommand(t)
	}
	if t.Type == "WORD" {
		switch value {
		case "if":
			return p.maybeRedirect(p.ifStatement(t), true)
		case "while", "until":
			return p.maybeRedirect(p.whileStatement(t), true)
		case "for", "select":
			return p.maybeRedirect(p.forStatement(t), true)
		case "function":
			return p.functionStatement(t)
		case "export", "declare", "typeset", "readonly", "local":
			return p.maybeRedirect(p.declaration(t))
		case "case":
			return p.maybeRedirect(p.caseStatement(t), true)
		case "unset", "unsetenv":
			return p.maybeRedirect(p.unset(t))
		}
	}
	p.l.restore(save)
	return p.simpleCommand()
}
func (p *parseState) maybeRedirect(node *Node, allowHerestring ...bool) *Node {
	redirects := []*Node{}
	for {
		p.l.skipBlanks()
		save := p.l.save()
		r := p.redirect(false)
		if r == nil {
			break
		}
		if r.Type == "herestring_redirect" && (len(allowHerestring) == 0 || !allowHerestring[0]) {
			p.l.restore(save)
			break
		}
		redirects = append(redirects, r)
	}
	if len(redirects) == 0 {
		return node
	}
	return p.node("redirected_statement", node.StartIndex, redirects[len(redirects)-1].EndIndex, append([]*Node{node}, redirects...)...)
}
func (p *parseState) simpleCommand() *Node {
	start := p.l.b
	assignments, preRedirects := []*Node{}, []*Node{}
	for {
		p.l.skipBlanks()
		if a := p.assignment(); a != nil {
			assignments = append(assignments, a)
			continue
		}
		if r := p.redirect(false); r != nil {
			preRedirects = append(preRedirects, r)
			continue
		}
		break
	}
	p.l.skipBlanks()
	save := p.l.save()
	nameTok := p.l.next("cmd")
	value := tokenText(nameTok)
	if nameTok.Type == "EOF" || nameTok.Type == "NEWLINE" || nameTok.Type == "COMMENT" || nameTok.Type == "OP" && value != "{" && value != "[" && value != "[[" || terminator(nameTok, "") {
		p.l.restore(save)
		if len(assignments) == 1 && len(preRedirects) == 0 {
			return assignments[0]
		}
		if len(preRedirects) > 0 && len(assignments) == 0 {
			return p.node("redirected_statement", preRedirects[0].StartIndex, preRedirects[len(preRedirects)-1].EndIndex, preRedirects...)
		}
		if len(assignments) > 1 && len(preRedirects) == 0 {
			return p.node("variable_assignments", assignments[0].StartIndex, assignments[len(assignments)-1].EndIndex, assignments...)
		}
		all := append(assignments, preRedirects...)
		if len(all) > 0 {
			return p.node("command", start, all[len(all)-1].EndIndex, all...)
		}
		return nil
	}
	p.l.restore(save)
	fnSave := p.l.save()
	name := p.word()
	p.l.skipBlanks()
	if name != nil && name.Type == "word" && p.l.peek(0) == '(' && p.l.peek(1) == ')' {
		open, close := p.leaf("(", p.l.next("cmd")), p.leaf(")", p.l.next("cmd"))
		p.l.skipBlanks()
		p.skipNewlines()
		if body := p.command(); body != nil {
			kids := []*Node{name, open, close}
			if body.Type == "redirected_statement" && len(body.Children) >= 2 && body.Children[0].Type == "compound_statement" {
				kids = append(kids, body.Children...)
			} else {
				kids = append(kids, body)
			}
			return p.node("function_definition", name.StartIndex, kids[len(kids)-1].EndIndex, kids...)
		}
	}
	p.l.restore(fnSave)
	name = p.word()
	if name == nil {
		return nil
	}
	commandName := p.node("command_name", name.StartIndex, name.EndIndex, name)
	args, redirects := []*Node{}, []*Node{}
	var heredoc *Node
	for {
		p.l.skipBlanks()
		if r := p.redirect(true); r != nil {
			if r.Type == "heredoc_redirect" {
				heredoc = r
				break
			} else if r.Type == "herestring_redirect" {
				args = append(args, r)
			} else {
				redirects = append(redirects, r)
			}
			continue
		}
		if len(redirects) > 0 {
			break
		}
		if p.stopToken == "]" && p.l.peek(0) == ']' {
			break
		}
		save := p.l.save()
		next := p.l.next("arg")
		v := tokenText(next)
		if next.Type == "EOF" || next.Type == "NEWLINE" || next.Type == "COMMENT" || next.Type == "OP" && (v == "|" || v == "|&" || v == "&&" || v == "||" || v == ";" || v == ";;" || v == ";&" || v == ";;&" || v == "&" || v == ")" || v == "}" || v == "))") {
			p.l.restore(save)
			break
		}
		p.l.restore(save)
		arg := p.word()
		if arg == nil {
			if p.l.peek(0) == '(' {
				opening := p.leaf("(", p.l.next("cmd"))
				body := p.statements(")")
				closing := p.l.next("cmd")
				var close *Node
				if closing.Type == "OP" && tokenText(closing) == ")" {
					close = p.leaf(")", closing)
				} else {
					close = p.node(")", opening.EndIndex, opening.EndIndex)
				}
				args = append(args, p.node("subshell", opening.StartIndex, close.EndIndex, append(append([]*Node{opening}, body...), close)...))
				continue
			}
			break
		}
		if arg.Type == "word" && string(arg.Text) == `"="` {
			arg = p.node("ERROR", arg.StartIndex, arg.EndIndex, arg)
		}
		if (arg.Type == "word" || arg.Type == "concatenation") && p.l.peek(0) == '(' && p.l.b == arg.EndIndex {
			arg = p.node("ERROR", arg.StartIndex, arg.EndIndex, arg)
		}
		args = append(args, arg)
	}
	children := append(append(append(assignments, preRedirects...), commandName), args...)
	command := p.node("command", children[0].StartIndex, children[len(children)-1].EndIndex, children...)
	if heredoc != nil {
		return p.finishHeredoc(command, heredoc, preRedirects, redirects)
	}
	if len(redirects) > 0 {
		return p.node("redirected_statement", command.StartIndex, redirects[len(redirects)-1].EndIndex, append([]*Node{command}, redirects...)...)
	}
	return command
}
func (p *parseState) word() *Node {
	p.l.skipBlanks()
	parts := []*Node{}
	for p.l.i < len(p.l.src) {
		c := p.l.peek(0)
		if c == -1 || containsASCII(" \t\n\r|&;()", c) {
			break
		}
		if c == '<' || c == '>' {
			if p.l.peek(1) == '(' {
				if sub := p.processSubstitution(); sub != nil {
					parts = append(parts, sub)
				}
				continue
			}
			break
		}
		if c == '"' {
			parts = append(parts, p.doubleQuoted())
			continue
		}
		if c == '\'' {
			parts = append(parts, p.leaf("raw_string", p.l.next("arg")))
			continue
		}
		if c == '$' {
			next := p.l.peek(1)
			if next == '\'' {
				parts = append(parts, p.leaf("ansi_c_string", p.l.next("arg")))
				continue
			}
			if next == '"' {
				begin := p.l.b
				p.l.advance()
				parts = append(parts, p.node("$", begin, p.l.b), p.doubleQuoted())
				continue
			}
			if next == '`' {
				p.l.advance()
				continue
			}
			if exp := p.dollar(); exp != nil {
				parts = append(parts, exp)
			}
			continue
		}
		if c == '`' {
			if p.inBacktick > 0 {
				break
			}
			if sub := p.backtick(); sub != nil {
				parts = append(parts, sub)
			}
			continue
		}
		if c == '{' {
			if be := p.braceExpression(); be != nil {
				parts = append(parts, be)
				continue
			}
			next := p.l.peek(1)
			if next == -1 || containsASCII(";|&\n) \t", next) {
				s := p.l.b
				p.l.advance()
				parts = append(parts, p.node("word", s, p.l.b))
				continue
			}
			if cat := p.braceConcatenation(); cat != nil {
				parts = append(parts, cat...)
				continue
			}
		}
		if c == '}' || c == '[' || c == ']' {
			start := p.l.b
			p.l.advance()
			parts = append(parts, p.node("word", start, p.l.b))
			continue
		}
		frag := p.bareWord()
		if frag == nil {
			break
		}
		if frag.Type == "word" && arithmeticBasePrefix(p.byteUnits(frag.StartIndex, frag.EndIndex)) && p.l.peek(0) == '$' && (p.l.peek(1) == '{' || p.l.peek(1) == '(') {
			if exp := p.dollar(); exp != nil {
				parts = append(parts, p.node("number", frag.StartIndex, exp.EndIndex, exp))
				continue
			}
		}
		parts = append(parts, frag)
	}
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return p.node("concatenation", parts[0].StartIndex, parts[len(parts)-1].EndIndex, parts...)
}
func (p *parseState) bareWord() *Node {
	start, startI := p.l.b, p.l.i
	for p.l.i < len(p.l.src) {
		c := p.l.peek(0)
		if c == '\\' {
			if p.l.i+1 >= len(p.l.src) {
				break
			}
			next := p.l.peek(1)
			if next == '\n' || next == '\r' && p.l.peek(2) == '\n' {
				break
			}
			p.l.advance()
			p.l.advance()
			continue
		}
		if c == -1 || containsASCII(" \t\n\r|&;()<>\"'$`{}[]", c) {
			break
		}
		p.l.advance()
	}
	if p.l.b == start {
		return nil
	}
	kind := "word"
	digits := startI
	if p.l.src[digits] == '-' {
		digits++
	}
	number := digits < p.l.i
	for i := digits; i < p.l.i; i++ {
		if i >= len(p.l.src) || !isDigit(int(p.l.src[i])) {
			number = false
			break
		}
	}
	if number {
		kind = "number"
	}
	return p.node(kind, start, p.l.b)
}
func (p *parseState) doubleQuoted() *Node {
	start := p.l.b
	p.l.advance()
	parts := []*Node{p.node("\"", start, p.l.b)}
	contentStart, contentI := p.l.b, p.l.i
	flush := func() {
		if p.l.b > contentStart {
			blank := true
			for i := contentI; i < p.l.i && i < len(p.l.src); i++ {
				if p.l.src[i] != ' ' && p.l.src[i] != '\t' {
					blank = false
					break
				}
			}
			if !blank {
				parts = append(parts, p.node("string_content", contentStart, p.l.b))
			}
		}
	}
	for p.l.i < len(p.l.src) {
		c := p.l.peek(0)
		if c == '"' {
			break
		}
		if c == '\\' && p.l.i+1 < len(p.l.src) {
			p.l.advance()
			p.l.advance()
			continue
		}
		if c == '\n' {
			flush()
			p.l.advance()
			contentStart, contentI = p.l.b, p.l.i
			continue
		}
		if c == '$' {
			next := p.l.peek(1)
			if next == '(' || next == '{' || isIdentStart(next) || containsASCII("?$@*#-!_", next) || isDigit(next) {
				flush()
				if exp := p.dollar(); exp != nil {
					parts = append(parts, exp)
				}
				contentStart, contentI = p.l.b, p.l.i
				continue
			}
			if next != '"' && next != -1 {
				flush()
				begin := p.l.b
				p.l.advance()
				parts = append(parts, p.node("$", begin, p.l.b))
				contentStart, contentI = p.l.b, p.l.i
				continue
			}
		}
		if c == '`' {
			flush()
			if sub := p.backtick(); sub != nil {
				parts = append(parts, sub)
			}
			contentStart, contentI = p.l.b, p.l.i
			continue
		}
		p.l.advance()
	}
	flush()
	var close *Node
	if p.l.peek(0) == '"' {
		begin := p.l.b
		p.l.advance()
		close = p.node("\"", begin, p.l.b)
	} else {
		close = p.node("\"", p.l.b, p.l.b)
	}
	parts = append(parts, close)
	return p.node("string", start, close.EndIndex, parts...)
}

func nodeDebug(n *Node) string {
	if n == nil {
		return "null"
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return fmt.Sprint(err)
	}
	return string(protocol.StringifyValidatedJavaScriptJSON(raw))
}
