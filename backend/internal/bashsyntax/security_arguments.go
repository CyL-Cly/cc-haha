package bashsyntax

import (
	"regexp"
	"strconv"
	"strings"
)

var regexpPS4Refs = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)

func walkTest(n *Node, argv *[]SecurityText, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) *SecurityResult {
	switch n.Type {
	case "unary_expression", "binary_expression", "negated_expression", "parenthesized_expression":
		for _, c := range n.Children {
			if c != nil {
				if f := walkTest(c, argv, commands, scope); f != nil {
					return f
				}
			}
		}
		return nil
	case "test_operator", "!", "(", ")", "&&", "||", "==", "=", "!=", "<", ">", "=~", "regex", "extglob_pattern":
		*argv = append(*argv, nodeText(n))
		return nil
	default:
		v, f := walkArgument(n, commands, scope)
		if f != nil {
			return f
		}
		*argv = append(*argv, v)
		return nil
	}
}
func walkRedirected(n *Node, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) *SecurityResult {
	redirects := []SecurityRedirect{}
	var inner *Node
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		switch c.Type {
		case "file_redirect":
			r, f := walkFileRedirect(c, commands, scope)
			if f != nil {
				return f
			}
			redirects = append(redirects, r)
		case "heredoc_redirect":
			if f := walkHeredoc(c); f != nil {
				return f
			}
		case "command", "pipeline", "list", "negated_command", "declaration_command", "unset_command":
			inner = c
		default:
			return tooComplex(c)
		}
	}
	if inner == nil {
		*commands = append(*commands, SecurityCommand{[]SecurityText{}, []SecurityEnvVar{}, redirects, nodeText(n)})
		return nil
	}
	before := len(*commands)
	if f := collectCommands(inner, commands, scope); f != nil {
		return f
	}
	if len(*commands) > before && len(redirects) > 0 {
		last := &(*commands)[len(*commands)-1]
		last.Redirects = append(last.Redirects, redirects...)
	}
	return nil
}
func walkFileRedirect(n *Node, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) (SecurityRedirect, *SecurityResult) {
	r := SecurityRedirect{}
	target := false
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		switch c.Type {
		case "file_descriptor":
			f, err := strconv.ParseFloat(string(nodeText(c)), 64)
			if err != nil {
				f = 0
			}
			r.FD = &f
		case ">", ">>", "<", ">&", "<&", ">|", "&>", "&>>", "<<<":
			r.Op = c.Type
		case "word", "number":
			if len(c.Children) > 0 || matches(`\{[^{}\s]*(,|\.\.)[^{}\s]*\}`, nodeText(c)) {
				return r, tooComplex(c)
			}
			r.Target = unescape(nodeText(c), false)
			target = true
		case "raw_string":
			text := nodeText(c)
			r.Target = textSlice(text, 1, textLen(text)-1)
			target = true
		case "string", "concatenation":
			value, f := walkArgument(c, commands, scope)
			if f != nil {
				return r, f
			}
			r.Target = value
			target = true
		default:
			return r, tooComplex(c)
		}
	}
	if r.Op == "" || !target {
		return r, complexReason("Unrecognized redirect shape", n.Type)
	}
	return r, nil
}
func walkHeredoc(n *Node) *SecurityResult {
	var start *SecurityText
	var body *Node
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		switch c.Type {
		case "heredoc_start":
			value := nodeText(c)
			start = &value
		case "heredoc_body":
			body = c
		case "<<", "<<-", "heredoc_end", "file_descriptor":
		default:
			return tooComplex(c)
		}
	}
	quoted := start != nil && ((strings.HasPrefix(string(*start), "'") && strings.HasSuffix(string(*start), "'")) || (strings.HasPrefix(string(*start), "\"") && strings.HasSuffix(string(*start), "\"")) || strings.HasPrefix(string(*start), "\\"))
	if !quoted {
		return complexReason("Heredoc with unquoted delimiter undergoes shell expansion", "heredoc_redirect")
	}
	if body != nil {
		for _, c := range body.Children {
			if c != nil && c.Type != "heredoc_content" {
				return tooComplex(c)
			}
		}
	}
	return nil
}
func walkString(n *Node, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) (SecurityText, *SecurityResult) {
	result := SecurityText("")
	cursor := -1
	dynamic, literal := false, false
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		if cursor != -1 && c.StartIndex > cursor && c.Type != "\"" {
			result += SecurityText(strings.Repeat("\n", c.StartIndex-cursor))
			literal = true
		}
		cursor = c.EndIndex
		switch c.Type {
		case "\"":
			cursor = c.EndIndex
		case "string_content":
			result += unescape(nodeText(c), true)
			literal = true
		case "$":
			result += "$"
			literal = true
		case "command_substitution":
			body, found, danger := safeCatHeredoc(c)
			if danger {
				return "", tooComplex(c)
			}
			if found {
				trim := SecurityText(strings.TrimRight(string(body), "\n"))
				if !strings.Contains(string(trim), "\n") {
					result += trim
				}
				literal = true
				break
			}
			if f := collectSubstitution(c, commands, scope); f != nil {
				return "", f
			}
			result += commandSubPlaceholder
			dynamic = true
		case "simple_expansion":
			v, f := resolveVariable(c, scope, true)
			if f != nil {
				return "", f
			}
			if v == variablePlaceholder {
				dynamic = true
			} else {
				literal = true
			}
			result += v
		case "arithmetic_expansion":
			if f := walkArithmetic(c); f != nil {
				return "", f
			}
			result += nodeText(c)
			literal = true
		default:
			return "", tooComplex(c)
		}
	}
	if dynamic && !literal || !literal && !dynamic && textLen(nodeText(n)) > 2 {
		return "", tooComplex(n)
	}
	return result, nil
}
func walkArithmetic(n *Node) *SecurityResult {
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		if len(c.Children) == 0 {
			if !matches(`^(?:[0-9]+|0[xX][0-9a-fA-F]+|[0-9]+#[0-9a-zA-Z]+|[-+*/%^&|~!<>=?:(),]+|<<|>>|\*\*|&&|\|\||[<>=!]=|\$\(\(|\)\))$`, nodeText(c)) {
				return complexReason("Arithmetic expansion references variable or non-literal: "+nodeText(c), "arithmetic_expansion")
			}
			continue
		}
		switch c.Type {
		case "binary_expression", "unary_expression", "ternary_expression", "parenthesized_expression":
			if f := walkArithmetic(c); f != nil {
				return f
			}
		default:
			return tooComplex(c)
		}
	}
	return nil
}
func safeCatHeredoc(n *Node) (SecurityText, bool, bool) {
	var statement *Node
	for _, c := range n.Children {
		if c == nil || c.Type == "$(" || c.Type == ")" {
			continue
		}
		if c.Type == "redirected_statement" && statement == nil {
			statement = c
		} else {
			return "", false, false
		}
	}
	if statement == nil {
		return "", false, false
	}
	sawCat := false
	var body *SecurityText
	for _, c := range statement.Children {
		if c == nil {
			continue
		}
		if c.Type == "command" {
			kids := []*Node{}
			for _, child := range c.Children {
				if child != nil {
					kids = append(kids, child)
				}
			}
			if len(kids) != 1 || kids[0].Type != "command_name" || nodeText(kids[0]) != "cat" {
				return "", false, false
			}
			sawCat = true
		} else if c.Type == "heredoc_redirect" {
			if walkHeredoc(c) != nil {
				return "", false, false
			}
			for _, child := range c.Children {
				if child != nil && child.Type == "heredoc_body" {
					value := nodeText(child)
					body = &value
				}
			}
		} else {
			return "", false, false
		}
	}
	if !sawCat || body == nil {
		return "", false, false
	}
	if matches(`/proc/.*/environ`, *body) || matches(`\bsystem\s*\(`, *body) {
		return "", false, true
	}
	return *body, true, false
}

type securityAssignment struct {
	name, value SecurityText
	append      bool
}

func walkAssignment(n *Node, commands *[]SecurityCommand, scope map[SecurityText]SecurityText) (securityAssignment, *SecurityResult) {
	ev := securityAssignment{}
	hasName := false
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		switch c.Type {
		case "variable_name":
			ev.name = nodeText(c)
			hasName = true
		case "=", "+=":
			ev.append = c.Type == "+="
		case "command_substitution":
			if f := collectSubstitution(c, commands, scope); f != nil {
				return ev, f
			}
			ev.value = commandSubPlaceholder
		case "simple_expansion":
			v, f := resolveVariable(c, scope, true)
			if f != nil {
				return ev, f
			}
			ev.value = v
		default:
			v, f := walkArgument(c, commands, scope)
			if f != nil {
				return ev, f
			}
			ev.value = v
		}
	}
	if !hasName {
		return ev, complexReason("Variable assignment without name", "variable_assignment")
	}
	if !matches(`^[A-Za-z_][A-Za-z0-9_]*$`, ev.name) {
		return ev, complexReason("Invalid variable name (bash treats as command): "+ev.name, "variable_assignment")
	}
	if ev.name == "IFS" {
		return ev, complexReason("IFS assignment changes word-splitting — cannot model statically", "variable_assignment")
	}
	if ev.name == "PS4" {
		if ev.append {
			return ev, complexReason("PS4 += cannot be statically verified — combine into a single PS4= assignment", "variable_assignment")
		}
		if containsPlaceholder(ev.value) {
			return ev, complexReason("PS4 value derived from cmdsub/variable — runtime unknowable", "variable_assignment")
		}
		remaining := regexpPS4Refs.ReplaceAllString(string(ev.value), "")
		if !matches(`^[A-Za-z0-9 _+:./=\[\]-]*$`, SecurityText(remaining)) {
			return ev, complexReason("PS4 value outside safe charset — only ${VAR} refs and [A-Za-z0-9 _+:.=/[]-] allowed", "variable_assignment")
		}
	}
	if strings.Contains(string(ev.value), "~") {
		return ev, complexReason("Tilde in assignment value — bash may expand at assignment time", "variable_assignment")
	}
	return ev, nil
}
func applyVariable(scope map[SecurityText]SecurityText, ev securityAssignment) {
	combined := ev.value
	if ev.append {
		combined = scope[ev.name] + ev.value
	}
	if containsPlaceholder(combined) {
		combined = variablePlaceholder
	}
	scope[ev.name] = combined
}
func resolveVariable(n *Node, scope map[SecurityText]SecurityText, inside bool) (SecurityText, *SecurityResult) {
	var name *SecurityText
	special := false
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		if c.Type == "variable_name" || c.Type == "special_variable_name" {
			value := nodeText(c)
			name = &value
			special = c.Type == "special_variable_name"
			break
		}
	}
	if name == nil {
		return "", tooComplex(n)
	}
	if value, ok := scope[*name]; ok {
		if containsPlaceholder(value) {
			if !inside {
				return "", tooComplex(n)
			}
			return variablePlaceholder, nil
		}
		if !inside && (value == "" || matches(`[ \t\n*?\[]`, value)) {
			return "", tooComplex(n)
		}
		return value, nil
	}
	if inside && (safeEnvVars[string(*name)] || special && (safeSpecial[string(*name)] || matches(`^[0-9]+$`, *name))) {
		return variablePlaceholder, nil
	}
	return "", tooComplex(n)
}
