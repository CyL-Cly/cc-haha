package bashsyntax

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SecurityText represents one JavaScript UTF-16 string. Surrogate units use
// otherwise unreachable scalar slots internally, so comparisons and JSON keep
// lone surrogates and astral units without Go's replacement-character loss.
type SecurityText string

func (s *SecurityText) UnmarshalJSON(raw []byte) error {
	u, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return err
	}
	*s = securityText(u)
	return nil
}

func securityText(units []uint16) SecurityText {
	r := make([]rune, len(units))
	for i, c := range units {
		r[i] = rune(c)
		if c >= 0xd800 && c <= 0xdfff {
			r[i] += 0x10000
		}
	}
	return SecurityText(string(r))
}
func (s SecurityText) MarshalJSON() ([]byte, error) {
	units := make([]uint16, 0, utf8.RuneCountInString(string(s)))
	for _, c := range s {
		if c >= 0x1d800 && c <= 0x1dfff {
			c -= 0x10000
		}
		units = append(units, uint16(c))
	}
	return protocol.QuoteJavaScriptUTF16(units), nil
}
func (s SecurityText) JSON() json.RawMessage { raw, _ := s.MarshalJSON(); return raw }
func nodeText(n *Node) SecurityText {
	u, _ := protocol.DecodeJavaScriptJSONString(n.Text)
	return securityText(u)
}
func textLen(s SecurityText) int { return utf8.RuneCountInString(string(s)) }
func textSlice(s SecurityText, start, end int) SecurityText {
	r := []rune(s)
	start = max(0, min(start, len(r)))
	end = max(start, min(end, len(r)))
	return SecurityText(string(r[start:end]))
}

type SecurityRedirect struct {
	Op     string       `json:"op"`
	Target SecurityText `json:"target"`
	FD     *float64     `json:"fd,omitempty"`
}
type SecurityEnvVar struct {
	Name  SecurityText `json:"name"`
	Value SecurityText `json:"value"`
}
type SecurityCommand struct {
	Argv      []SecurityText     `json:"argv"`
	EnvVars   []SecurityEnvVar   `json:"envVars"`
	Redirects []SecurityRedirect `json:"redirects"`
	Text      SecurityText       `json:"text"`
}
type SecurityResult struct {
	Kind     string            `json:"kind"`
	Commands []SecurityCommand `json:"commands,omitempty"`
	Reason   SecurityText      `json:"reason,omitempty"`
	NodeType string            `json:"nodeType,omitempty"`
}

func (r SecurityResult) MarshalJSON() ([]byte, error) {
	if r.Kind == "simple" {
		return json.Marshal(struct {
			Kind     string            `json:"kind"`
			Commands []SecurityCommand `json:"commands"`
		}{r.Kind, r.Commands})
	}
	type alias SecurityResult
	return json.Marshal(alias(r))
}

type SecurityOptions struct {
	Enabled bool
	Parse   ParseOptions
}

const commandSubPlaceholder SecurityText = "__CMDSUB_OUTPUT__"
const variablePlaceholder SecurityText = "__TRACKED_VAR__"

var dangerousTypes = stringSet("command_substitution process_substitution expansion simple_expansion brace_expression subshell compound_statement for_statement while_statement until_statement if_statement case_statement function_definition test_command ansi_c_string translated_string herestring_redirect heredoc_redirect")
var structuralTypes = stringSet("program list pipeline redirected_statement")
var separatorTypes = stringSet("&& || | ; & |& \n")
var safeEnvVars = stringSet("HOME PWD OLDPWD USER LOGNAME SHELL PATH HOSTNAME UID EUID PPID RANDOM SECONDS LINENO TMPDIR BASH_VERSION BASHPID SHLVL HISTFILE IFS")
var safeSpecial = stringSet("? $ ! # 0 -")

func stringSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, k := range strings.Fields(s) {
		out[k] = true
	}
	return out
}
func complexReason(reason SecurityText, kind string) *SecurityResult {
	return &SecurityResult{Kind: "too-complex", Reason: reason, NodeType: kind}
}
func tooComplex(n *Node) *SecurityResult {
	reason := SecurityText("Unhandled node type: " + n.Type)
	if n.Type == "ERROR" {
		reason = "Parse error"
	} else if dangerousTypes[n.Type] {
		reason = SecurityText("Contains " + n.Type)
	}
	return complexReason(reason, n.Type)
}
func containsPlaceholder(s SecurityText) bool {
	return strings.Contains(string(s), string(commandSubPlaceholder)) || strings.Contains(string(s), string(variablePlaceholder))
}

var securityRegexCache sync.Map

func matches(pattern string, s SecurityText) bool {
	value, ok := securityRegexCache.Load(pattern)
	if !ok {
		// These are fixed original expressions. Translate only generic
		// non-u ECMAScript dot and whitespace primitives; Go's defaults
		// otherwise differ on CR/Unicode line terminators and NBSP.
		const whitespace = `\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`
		var b strings.Builder
		inClass := false
		for i := 0; i < len(pattern); i++ {
			c := pattern[i]
			if c == '\\' && i+1 < len(pattern) {
				i++
				if pattern[i] == 's' {
					if !inClass {
						b.WriteByte('[')
					}
					b.WriteString(whitespace)
					if !inClass {
						b.WriteByte(']')
					}
				} else {
					b.WriteByte('\\')
					b.WriteByte(pattern[i])
				}
				continue
			}
			if c == '[' {
				inClass = true
			} else if c == ']' {
				inClass = false
			}
			if c == '.' && !inClass {
				b.WriteString(`[^\n\r\x{2028}\x{2029}]`)
			} else {
				b.WriteByte(c)
			}
		}
		value, _ = securityRegexCache.LoadOrStore(pattern, regexp.MustCompile(b.String()))
	}
	return value.(*regexp.Regexp).MatchString(string(s))
}

// ParseForSecurity preserves the original feature-off and 10K UTF-16 boundary.
// A parser resource failure is distinct from an unavailable parser.
func ParseForSecurity(ctx context.Context, raw json.RawMessage, options SecurityOptions) (SecurityResult, error) {
	u, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return SecurityResult{}, err
	}
	if len(u) == 0 {
		return SecurityResult{Kind: "simple", Commands: []SecurityCommand{}}, nil
	}
	if len(u) > 10000 || !options.Enabled {
		return SecurityResult{Kind: "parse-unavailable"}, nil
	}
	root, err := Parse(ctx, raw, options.Parse)
	if err != nil {
		if ctx.Err() != nil {
			return SecurityResult{}, ctx.Err()
		}
		return SecurityFromAST(raw, nil, true)
	}
	return SecurityFromAST(raw, root, false)
}
func SecurityFromAST(raw json.RawMessage, root *Node, aborted bool) (SecurityResult, error) {
	u, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return SecurityResult{}, err
	}
	text := securityText(u)
	checks := []struct{ re, reason string }{{`[\x00-\x08\x0B-\x1F\x7F]`, "Contains control characters"}, {`[\x{00a0}\x{1680}\x{2000}-\x{200b}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`, "Contains Unicode whitespace"}, {`\\[ \t]|[^ \t\n\\]\\\n`, "Contains backslash-escaped whitespace"}, {`~\[`, "Contains zsh ~[ dynamic directory syntax"}, {`(?:^|[\t\n\r\f\v ;&|])=[a-zA-Z_]`, "Contains zsh =cmd equals expansion"}}
	for _, check := range checks {
		if matches(check.re, text) {
			return *complexReason(SecurityText(check.reason), ""), nil
		}
	}
	if matches(`\{[^}]*['"]`, maskQuotedBraces(text)) {
		return *complexReason("Contains brace with quote character (expansion obfuscation)", ""), nil
	}
	if protocol.TrimJavaScriptString(string(text)) == "" {
		return SecurityResult{Kind: "simple", Commands: []SecurityCommand{}}, nil
	}
	if aborted {
		return *complexReason("Parser aborted (timeout or resource limit) — possible adversarial input", "PARSE_ABORT"), nil
	}
	if root == nil {
		return SecurityResult{Kind: "parse-unavailable"}, nil
	}
	commands := []SecurityCommand{}
	if failure := collectCommands(root, &commands, map[SecurityText]SecurityText{}); failure != nil {
		return *failure, nil
	}
	return SecurityResult{Kind: "simple", Commands: commands}, nil
}
func maskQuotedBraces(s SecurityText) SecurityText {
	r := []rune(s)
	single, double := false, false
	for i := 0; i < len(r); i++ {
		c := r[i]
		if single {
			if c == '\'' {
				single = false
			}
			if c == '{' {
				r[i] = ' '
			}
		} else if double {
			if c == '\\' && i+1 < len(r) && (r[i+1] == '"' || r[i+1] == '\\') {
				i++
			} else {
				if c == '"' {
					double = false
				}
				if c == '{' {
					r[i] = ' '
				}
			}
		} else {
			if c == '\\' && i+1 < len(r) {
				i++
			} else if c == '\'' {
				single = true
			} else if c == '"' {
				double = true
			}
		}
	}
	return SecurityText(string(r))
}
func copyScope(scope map[SecurityText]SecurityText) map[SecurityText]SecurityText {
	out := make(map[SecurityText]SecurityText, len(scope))
	for k, v := range scope {
		out[k] = v
	}
	return out
}

func collectCommands(n *Node, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) *SecurityResult {
	switch n.Type {
	case "command":
		cmd, failure := walkCommand(n, commands, scope)
		if failure != nil {
			return failure
		}
		*commands = append(*commands, cmd)
		return nil
	case "redirected_statement":
		return walkRedirected(n, commands, scope)
	case "comment":
		return nil
	case "negated_command":
		for _, c := range n.Children {
			if c != nil && c.Type != "!" {
				return collectCommands(c, commands, scope)
			}
		}
		return nil
	case "declaration_command":
		return collectDeclaration(n, commands, scope)
	case "variable_assignment":
		ev, failure := walkAssignment(n, commands, scope)
		if failure != nil {
			return failure
		}
		applyVariable(scope, ev)
		return nil
	case "for_statement":
		return collectFor(n, commands, scope)
	case "if_statement", "while_statement":
		return collectConditional(n, commands, scope)
	case "subshell":
		inner := copyScope(scope)
		for _, c := range n.Children {
			if c != nil && c.Type != "(" && c.Type != ")" {
				if failure := collectCommands(c, commands, inner); failure != nil {
					return failure
				}
			}
		}
		return nil
	case "test_command":
		argv := []SecurityText{"[["}
		for _, c := range n.Children {
			if c != nil && c.Type != "[[" && c.Type != "]]" && c.Type != "[" && c.Type != "]" {
				if failure := walkTest(c, &argv, commands, scope); failure != nil {
					return failure
				}
			}
		}
		*commands = append(*commands, SecurityCommand{argv, []SecurityEnvVar{}, []SecurityRedirect{}, nodeText(n)})
		return nil
	case "unset_command":
		argv := []SecurityText{}
		for _, c := range n.Children {
			if c == nil {
				continue
			}
			switch c.Type {
			case "unset":
				argv = append(argv, nodeText(c))
			case "variable_name":
				value := nodeText(c)
				argv = append(argv, value)
				delete(scope, value)
			case "word":
				v, f := walkArgument(c, commands, scope)
				if f != nil {
					return f
				}
				argv = append(argv, v)
			default:
				return tooComplex(c)
			}
		}
		*commands = append(*commands, SecurityCommand{argv, []SecurityEnvVar{}, []SecurityRedirect{}, nodeText(n)})
		return nil
	}
	if structuralTypes[n.Type] {
		pipeline := n.Type == "pipeline"
		var snapshot map[SecurityText]SecurityText
		if !pipeline {
			for _, c := range n.Children {
				if c != nil && (c.Type == "||" || c.Type == "&") {
					snapshot = copyScope(scope)
					break
				}
			}
		}
		current := scope
		if pipeline {
			current = copyScope(scope)
		}
		for _, c := range n.Children {
			if c == nil {
				continue
			}
			if separatorTypes[c.Type] || c.Type == "\n" {
				if c.Type == "||" || c.Type == "|" || c.Type == "|&" || c.Type == "&" {
					reset := snapshot
					if reset == nil {
						reset = scope
					}
					current = copyScope(reset)
				}
				continue
			}
			if failure := collectCommands(c, commands, current); failure != nil {
				return failure
			}
		}
		return nil
	}
	return tooComplex(n)
}

func collectDeclaration(n *Node, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) *SecurityResult {
	argv := []SecurityText{}
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		switch c.Type {
		case "export", "local", "readonly", "declare", "typeset", "variable_name":
			argv = append(argv, nodeText(c))
		case "word", "number", "raw_string", "string", "concatenation":
			v, f := walkArgument(c, commands, scope)
			if f != nil {
				return f
			}
			decl := len(argv) > 0 && (argv[0] == "declare" || argv[0] == "typeset" || argv[0] == "local")
			if decl && matches(`^-[a-zA-Z]*[niaA]`, v) {
				return complexReason("declare flag "+v+" changes assignment semantics (nameref/integer/array)", "declaration_command")
			}
			if decl && !strings.HasPrefix(string(v), "-") && matches(`^[^=]*\[`, v) {
				return complexReason("declare positional '"+v+"' contains array subscript — bash evaluates $(cmd) in subscripts", "declaration_command")
			}
			argv = append(argv, v)
		case "variable_assignment":
			ev, f := walkAssignment(c, commands, scope)
			if f != nil {
				return f
			}
			applyVariable(scope, ev)
			argv = append(argv, ev.name+"="+ev.value)
		default:
			return tooComplex(c)
		}
	}
	*commands = append(*commands, SecurityCommand{argv, []SecurityEnvVar{}, []SecurityRedirect{}, nodeText(n)})
	return nil
}
func collectFor(n *Node, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) *SecurityResult {
	var variable *SecurityText
	var body *Node
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		switch c.Type {
		case "variable_name":
			v := nodeText(c)
			variable = &v
		case "do_group":
			body = c
		case "for", "in", "select", ";":
		case "command_substitution":
			if f := collectSubstitution(c, commands, scope); f != nil {
				return f
			}
		default:
			if _, f := walkArgument(c, commands, scope); f != nil {
				return f
			}
		}
	}
	if variable == nil || body == nil {
		return tooComplex(n)
	}
	if *variable == "PS4" || *variable == "IFS" {
		return complexReason(*variable+" as loop variable bypasses assignment validation", "for_statement")
	}
	scope[*variable] = variablePlaceholder
	inner := copyScope(scope)
	for _, c := range body.Children {
		if c != nil && c.Type != "do" && c.Type != "done" && c.Type != ";" {
			if f := collectCommands(c, commands, inner); f != nil {
				return f
			}
		}
	}
	return nil
}
func collectConditional(n *Node, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) *SecurityResult {
	then := false
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		switch c.Type {
		case "if", "fi", "else", "elif", "while", "until", ";":
			continue
		case "then":
			then = true
			continue
		case "do_group", "elif_clause", "else_clause":
			inner := copyScope(scope)
			for _, child := range c.Children {
				if child == nil || child.Type == "do" || child.Type == "done" || child.Type == ";" || child.Type == "elif" || child.Type == "else" || child.Type == "then" {
					continue
				}
				if f := collectCommands(child, commands, inner); f != nil {
					return f
				}
			}
			continue
		}
		target := scope
		if then {
			target = copyScope(scope)
		}
		before := len(*commands)
		if f := collectCommands(c, commands, target); f != nil {
			return f
		}
		if !then {
			for _, cmd := range (*commands)[before:] {
				if len(cmd.Argv) == 0 || cmd.Argv[0] != "read" {
					continue
				}
				for _, arg := range cmd.Argv[1:] {
					if !matches(`^[A-Za-z_][A-Za-z0-9_]*$`, arg) {
						continue
					}
					existing, ok := scope[arg]
					if ok && !containsPlaceholder(existing) {
						return complexReason("'read "+arg+"' in condition may not execute (||/pipeline/subshell); cannot prove it overwrites tracked literal '"+existing+"'", "if_statement")
					}
					scope[arg] = variablePlaceholder
				}
			}
		}
	}
	return nil
}

func walkCommand(n *Node, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) (SecurityCommand, *SecurityResult) {
	cmd := SecurityCommand{[]SecurityText{}, []SecurityEnvVar{}, []SecurityRedirect{}, nodeText(n)}
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		switch c.Type {
		case "variable_assignment":
			ev, f := walkAssignment(c, commands, scope)
			if f != nil {
				return cmd, f
			}
			cmd.EnvVars = append(cmd.EnvVars, SecurityEnvVar{ev.name, ev.value})
		case "command_name":
			arg := c
			if len(c.Children) > 0 && c.Children[0] != nil {
				arg = c.Children[0]
			}
			v, f := walkArgument(arg, commands, scope)
			if f != nil {
				return cmd, f
			}
			cmd.Argv = append(cmd.Argv, v)
		case "word", "number", "raw_string", "string", "concatenation", "arithmetic_expansion", "simple_expansion":
			v, f := walkArgument(c, commands, scope)
			if f != nil {
				return cmd, f
			}
			cmd.Argv = append(cmd.Argv, v)
		case "file_redirect":
			r, f := walkFileRedirect(c, commands, scope)
			if f != nil {
				return cmd, f
			}
			cmd.Redirects = append(cmd.Redirects, r)
		case "herestring_redirect":
			for _, child := range c.Children {
				if child == nil || child.Type == "<<<" {
					continue
				}
				v, f := walkArgument(child, commands, scope)
				if f != nil {
					return cmd, f
				}
				if matches(`\n[ \t]*#`, v) {
					return cmd, tooComplex(child)
				}
			}
		default:
			return cmd, tooComplex(c)
		}
	}
	if matches(`\$[A-Za-z_]|\n`, cmd.Text) {
		parts := make([]string, len(cmd.Argv))
		for i, arg := range cmd.Argv {
			if arg == "" || matches("[\"'\\\\ \t\n$`;|&<>(){}*?\\[\\]~#]", arg) {
				arg = "'" + SecurityText(strings.ReplaceAll(string(arg), "'", "'\\''")) + "'"
			}
			parts[i] = string(arg)
		}
		cmd.Text = SecurityText(strings.Join(parts, " "))
	}
	return cmd, nil
}
func collectSubstitution(n *Node, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) *SecurityResult {
	inner := copyScope(scope)
	for _, c := range n.Children {
		if c == nil || c.Type == "$(" || c.Type == "`" || c.Type == ")" {
			continue
		}
		if f := collectCommands(c, commands, inner); f != nil {
			return f
		}
	}
	return nil
}
func unescape(s SecurityText, quoted bool) SecurityText {
	u := []rune(s)
	out := make([]rune, 0, len(u))
	for i := 0; i < len(u); i++ {
		if u[i] == '\\' && i+1 < len(u) && ((!quoted && u[i+1] != '\n' && u[i+1] != '\r' && u[i+1] != 0x2028 && u[i+1] != 0x2029) || quoted && strings.ContainsRune("$`\"\\", u[i+1])) {
			i++
		}
		out = append(out, u[i])
	}
	return SecurityText(string(out))
}
func walkArgument(n *Node, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) (SecurityText, *SecurityResult) {
	if n == nil {
		return "", complexReason("Null argument node", "")
	}
	text := nodeText(n)
	switch n.Type {
	case "word":
		if matches(`\{[^{}\s]*(,|\.\.)[^{}\s]*\}`, text) {
			return "", complexReason("Word contains brace expansion syntax", "word")
		}
		return unescape(text, false), nil
	case "number":
		if len(n.Children) > 0 {
			kind := ""
			if n.Children[0] != nil {
				kind = n.Children[0].Type
			}
			return "", complexReason("Number node contains expansion (NN# arithmetic base syntax)", kind)
		}
		return text, nil
	case "raw_string":
		return textSlice(text, 1, textLen(text)-1), nil
	case "string":
		return walkString(n, commands, scope)
	case "concatenation":
		if matches(`\{[^{}\s]*(,|\.\.)[^{}\s]*\}`, text) {
			return "", complexReason("Brace expansion", "concatenation")
		}
		value := SecurityText("")
		for _, c := range n.Children {
			if c == nil {
				continue
			}
			v, f := walkArgument(c, commands, scope)
			if f != nil {
				return "", f
			}
			value += v
		}
		return value, nil
	case "arithmetic_expansion":
		if f := walkArithmetic(n); f != nil {
			return "", f
		}
		return text, nil
	case "simple_expansion":
		return resolveVariable(n, scope, false)
	default:
		return "", tooComplex(n)
	}
}
