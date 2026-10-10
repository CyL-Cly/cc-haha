package sessionrewind

import "strings"

func sedFlagsAllowed(flags, allowed []string) bool {
	for _, flag := range flags {
		if strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") && len(flag) > 2 {
			for _, r := range flag[1:] {
				if !member("-"+string(r), allowed...) {
					return false
				}
			}
		} else if !member(flag, allowed...) {
			return false
		}
	}
	return true
}
func (c *RecordedCommandClassifier) sedAllowed(command string) (bool, error) {
	re, _ := regexpNative(`^\s*sed\s+`)
	match, _ := re.FindStringMatch(command)
	if match == nil {
		return false, nil
	}
	without := command[len(match.String()):]
	if jsMatch(`-e[wWe]|-w[eE]`, without) {
		return false, nil
	}
	tokens, err := c.parse(without, false)
	if err != nil {
		return false, nil
	}
	expressions, flags := []string{}, []string{}
	foundE, foundExpr := false, false
	for i := 0; i < len(tokens); i++ {
		arg := tokens[i]
		if !arg.isString {
			continue
		}
		if strings.HasPrefix(arg.text, "-") && arg.text != "--" {
			flags = append(flags, arg.text)
		}
	}
	for i := 0; i < len(tokens); i++ {
		arg := tokens[i]
		if !arg.isString {
			continue
		}
		value := arg.text
		if (value == "-e" || value == "--expression") && i+1 < len(tokens) {
			foundE = true
			if tokens[i+1].isString {
				expressions = append(expressions, tokens[i+1].text)
				i++
			}
			continue
		}
		if strings.HasPrefix(value, "--expression=") {
			foundE = true
			expressions = append(expressions, strings.TrimPrefix(value, "--expression="))
			continue
		}
		if strings.HasPrefix(value, "-e=") {
			foundE = true
			expressions = append(expressions, strings.TrimPrefix(value, "-e="))
			continue
		}
		if strings.HasPrefix(value, "-") {
			continue
		}
		if !foundE && !foundExpr {
			expressions = append(expressions, value)
			foundExpr = true
			continue
		}
		break
	}
	fileArgs, argCount, hasE := false, 0, false
	for i := 0; i < len(tokens); i++ {
		arg := tokens[i]
		if arg.op == "glob" {
			fileArgs = true
			break
		}
		if !arg.isString {
			continue
		}
		value := arg.text
		if (value == "-e" || value == "--expression") && i+1 < len(tokens) {
			hasE = true
			i++
			continue
		}
		if strings.HasPrefix(value, "--expression=") || strings.HasPrefix(value, "-e=") {
			hasE = true
			continue
		}
		if strings.HasPrefix(value, "-") {
			continue
		}
		argCount++
		if hasE || argCount > 1 {
			fileArgs = true
			break
		}
	}
	printing := len(expressions) > 0 && sedFlagsAllowed(flags, []string{"-n", "--quiet", "--silent", "-E", "--regexp-extended", "-r", "-z", "--zero-terminated", "--posix"})
	hasN := false
	for _, flag := range flags {
		if member(flag, "-n", "--quiet", "--silent") || strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") && strings.Contains(flag, "n") {
			hasN = true
		}
	}
	printing = printing && hasN
	for _, expr := range expressions {
		for _, part := range strings.Split(expr, ";") {
			if !jsMatch(`^(?:\d+|\d+,\d+)?p$`, jsTrimCommand(part)) {
				printing = false
			}
		}
	}
	substitution := !fileArgs && len(expressions) == 1 && sedFlagsAllowed(flags, []string{"-E", "--regexp-extended", "-r", "--posix"})
	if substitution {
		expr := jsTrimCommand(expressions[0])
		substitution = strings.HasPrefix(expr, "s/") && !strings.Contains(expr, "\n")
		if substitution {
			rest := expr[2:]
			count, last := 0, -1
			for i := 0; i < len(rest); i++ {
				if rest[i] == '\\' {
					i++
					continue
				}
				if rest[i] == '/' {
					count++
					last = i
				}
			}
			substitution = count == 2 && jsMatch(`^[gpimIM]*[1-9]?[gpimIM]*$`, rest[last+1:])
		}
	}
	if !printing && !substitution {
		return false, nil
	}
	for _, expr := range expressions {
		if substitution && strings.Contains(expr, ";") {
			return false, nil
		}
		if sedDangerous(expr) {
			return false, nil
		}
	}
	return true, nil
}
func sedDangerous(expr string) bool {
	expr = jsTrimCommand(expr)
	if expr == "" {
		return false
	}
	if jsMatch(`[^\x01-\x7F]`, expr) || strings.ContainsAny(expr, "{}\n") {
		return true
	}
	if i := strings.IndexByte(expr, '#'); i >= 0 && !(i > 0 && expr[i-1] == 's') {
		return true
	}
	for _, pattern := range []string{`^!`, `[/\d$]!`, `\d\s*~\s*\d|,\s*~\s*\d|\$\s*~\s*\d`, `^,`, `,\s*[+-]`, `s\\`, `\\[|#%@]`, `\\/.*[wW]`, `/[^/]*\s+[wWeE]`} {
		if jsMatch(pattern, expr) {
			return true
		}
	}
	if strings.HasPrefix(expr, "s/") && !jsMatch(`^s/[^/]*/[^/]*/[^/]*$`, expr) {
		return true
	}
	if jsMatch(`^s.`, expr) && jsMatch(`[wWeE]$`, expr) && !jsMatch(`^s([^\\\n]).*?\1.*?\1[^wWeE]*$`, expr) {
		return true
	}
	for _, prefix := range []string{`^`, `^\d+\s*`, `^\$\s*`, `^/[^/]*/[IMim]*\s*`, `^\d+,\d+\s*`, `^\d+,\$\s*`, `^/[^/]*/[IMim]*,/[^/]*/[IMim]*\s*`} {
		if jsMatch(prefix+`[wW]\s*\S+`, expr) || jsMatch(prefix+`e`, expr) {
			return true
		}
	}
	re, _ := regexpNative(`s([^\\\n]).*?\1.*?\1(.*?)$`)
	match, _ := re.FindStringMatch(expr)
	if match != nil && strings.ContainsAny(match.GroupByNumber(2).String(), "wWeE") {
		return true
	}
	return jsMatch(`y([^\\\n])`, expr) && jsMatch(`[wWeE]`, expr)
}
