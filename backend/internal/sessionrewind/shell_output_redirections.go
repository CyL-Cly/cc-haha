package sessionrewind

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

type ShellOutputRedirection struct{ Target, Operator string }
type ShellOutputRedirections struct {
	CommandWithoutRedirections string
	Redirections               []ShellOutputRedirection
	HasDangerousRedirection    bool
}

func shellFD(t shellToken) bool { return t.isString && jsMatch(`^\d+$`, jsTrimCommand(t.text)) }
func shellExpansion(t shellToken) bool {
	if t.op == "glob" {
		return true
	}
	if !t.isString || t.text == "" {
		return false
	}
	return strings.ContainsAny(t.text, "$%`*?[{") || strings.HasPrefix(t.text, "!") || strings.HasPrefix(t.text, "=") || strings.HasPrefix(t.text, "~")
}
func shellText(value string) shellToken { return shellToken{text: value, isString: true} }

// ExtractShellOutputRedirections reuses the original shared parser and heredoc
// producer. It ports commands.ts' reconstruction for rule matching; it is not
// a new execution parser or a permission grant.
func (c *RecordedCommandClassifier) ExtractShellOutputRedirections(command string) (ShellOutputRedirections, error) {
	out := ShellOutputRedirections{CommandWithoutRedirections: command, Redirections: []ShellOutputRedirection{}}
	heredocs, e := nativeHeredocs(command, false)
	if e != nil {
		return out, e
	}
	processed := joinContinuations(heredocs.processed)
	tokens, e := c.parse(processed, true)
	if e != nil {
		out.HasDangerousRedirection = true
		return out, nil
	}
	get := func(i int) shellToken {
		if i < 0 || i >= len(tokens) {
			return shellToken{}
		}
		return tokens[i]
	}
	type paren struct {
		index int
		start bool
	}
	stack := []paren{}
	redirected := map[int]bool{}
	for i, t := range tokens {
		if t.op == "(" {
			prev := get(i - 1)
			stack = append(stack, paren{i, i == 0 || member(prev.op, "&&", "||", ";", "|")})
		} else if t.op == ")" && len(stack) > 0 {
			opening := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if opening.start && member(get(i+1).op, ">", ">>") {
				redirected[opening.index] = true
				redirected[i] = true
			}
		}
	}
	kept := []shellToken{}
	depth := 0
	fd := func(number, operator string, target shellToken, skip int) (int, bool) {
		if len(kept) > 0 {
			kept = kept[:len(kept)-1]
		}
		isFD := shellFD(target)
		if !isFD && shellExpansion(target) {
			return 0, true
		}
		if simpleOutputTarget(target) && !jsMatch(`^\d+$`, target.text) {
			out.Redirections = append(out.Redirections, ShellOutputRedirection{target.text, operator})
			if number != "1" {
				kept = append(kept, shellText(number+operator), target)
			}
			return skip, false
		}
		if number != "1" {
			kept = append(kept, shellText(number+operator))
			if target.isString && target.text != "" || target.op != "" || target.isComment {
				kept = append(kept, target)
				return 1, false
			}
		}
		return 0, false
	}
	for i := 0; i < len(tokens); i++ {
		t, prev, next, after, third := get(i), get(i-1), get(i+1), get(i+2), get(i+3)
		if t.isString && t.text == "" {
			continue
		}
		if member(t.op, "(", ")") && redirected[i] {
			continue
		}
		if t.op == "(" && prev.isString && strings.HasSuffix(prev.text, "$") {
			depth++
		} else if t.op == ")" && depth > 0 {
			depth--
		}
		skip, danger := 0, false
		capture := func(target shellToken, operator string, n int) {
			out.Redirections = append(out.Redirections, ShellOutputRedirection{target.text, operator})
			skip = n
		}
		if depth == 0 && member(t.op, ">", ">>") {
			forced := next.isString && next.text == "!" || next.op == "|"
			bang := next.isString && len(next.text) > 1 && next.text[0] == '!' && !strings.ContainsRune("!-?", rune(next.text[1])) && !(next.text[1] >= '0' && next.text[1] <= '9')
			switch {
			case shellFD(prev):
				if forced && (simpleOutputTarget(after) || shellExpansion(after)) {
					if shellExpansion(after) && !simpleOutputTarget(after) {
						danger = true
					} else {
						skip, danger = fd(jsTrimCommand(prev.text), t.op, after, 2)
					}
				} else if bang {
					target := shellText(next.text[1:])
					if shellExpansion(target) {
						danger = true
					} else {
						skip, danger = fd(jsTrimCommand(prev.text), t.op, target, 1)
					}
				} else {
					skip, danger = fd(jsTrimCommand(prev.text), t.op, next, 1)
				}
			case forced:
				if simpleOutputTarget(after) {
					capture(after, t.op, 2)
				} else if shellExpansion(after) {
					danger = true
				}
			case bang:
				target := shellText(next.text[1:])
				if shellExpansion(target) {
					danger = true
				} else {
					capture(target, t.op, 1)
				}
			case next.op == "&":
				if after.isString && after.text == "!" || after.op == "|" {
					if simpleOutputTarget(third) {
						capture(third, t.op, 3)
					} else if shellExpansion(third) {
						danger = true
					}
				} else if simpleOutputTarget(after) {
					capture(after, t.op, 2)
				} else if shellExpansion(after) {
					danger = true
				}
			default:
				if simpleOutputTarget(next) {
					capture(next, t.op, 1)
				} else if shellExpansion(next) {
					danger = true
				}
			}
		} else if depth == 0 && t.op == ">&" {
			if shellFD(prev) && shellFD(next) {
			} else if next.op == "|" || next.isString && next.text == "!" {
				if simpleOutputTarget(after) {
					capture(after, ">", 2)
				} else if shellExpansion(after) {
					danger = true
				}
			} else if simpleOutputTarget(next) && !shellFD(next) {
				capture(next, ">", 1)
			} else if !shellFD(next) && shellExpansion(next) {
				danger = true
			}
		}
		out.HasDangerousRedirection = out.HasDangerousRedirection || danger
		if skip > 0 {
			i += skip
			continue
		}
		kept = append(kept, t)
	}
	out.CommandWithoutRedirections = restoreNativeHeredocs(reconstructShellCommand(kept, processed), heredocs.items)
	return out, nil
}
func reconstructShellCommand(kept []shellToken, original string) string {
	if len(kept) == 0 {
		return original
	}
	result := ""
	depth := 0
	processSub := false
	get := func(i int) shellToken {
		if i < 0 || i >= len(kept) {
			return shellToken{}
		}
		return kept[i]
	}
	add := func(token string, noSpace bool) {
		if result == "" || noSpace {
			result += token
		} else {
			result += " " + token
		}
	}
	detect := func(prev shellToken, index int) bool {
		if !prev.isString || prev.text == "" {
			return false
		}
		if prev.text == "$" {
			return true
		}
		if strings.HasSuffix(prev.text, "$") {
			if strings.Contains(prev.text, "=") && strings.HasSuffix(prev.text, "=$") {
				return true
			}
			depth := 1
			for j := index + 1; j < len(kept) && depth > 0; j++ {
				if kept[j].op == "(" {
					depth++
				}
				if kept[j].op == ")" {
					depth--
					if depth == 0 {
						after := get(j + 1)
						return after.isString && after.text != "" && !strings.HasPrefix(after.text, " ")
					}
				}
			}
		}
		return false
	}
	for i := 0; i < len(kept); i++ {
		t, prev, next := get(i), get(i-1), get(i+1)
		if t.isString {
			str := t.text
			if strings.ContainsAny(str, "|&;") {
				str = `"` + str + `"`
			} else if !jsMatch(`^\d+>>?$`, str) && (jsMatch(`\s`, str) || len(str) == 1 && strings.ContainsAny(str, "><|&;()")) {
				str = teamspawn.ShellQuote(str)
			}
			if strings.HasSuffix(result, "<(") {
				result += " " + str
			} else {
				add(str, strings.HasSuffix(result, "(") || prev.isString && prev.text == "$" || prev.op == ")")
			}
			continue
		}
		if t.op == "glob" {
			add(t.pattern, false)
			continue
		}
		if t.op == ">&" && prev.isString && jsMatch(`^\d+$`, prev.text) && next.isString && jsMatch(`^\d+$`, next.text) {
			index := strings.LastIndex(result, prev.text)
			result = result[:index] + prev.text + t.op + next.text
			i++
			continue
		}
		if t.op == "<" && next.op == "<" && get(i+2).isString && get(i+2).text != "" {
			add(get(i+2).text, false)
			i += 2
			continue
		}
		if t.op == "<<<" {
			add(t.op, false)
			continue
		}
		if t.op == "(" {
			if detect(prev, i) || depth > 0 {
				depth++
				result = strings.TrimSuffix(result, " ") + "("
			} else {
				add("(", strings.HasSuffix(result, "<(") || strings.HasSuffix(result, "("))
			}
			continue
		}
		if t.op == ")" {
			if processSub {
				processSub = false
			} else if depth > 0 {
				depth--
			}
			result += ")"
			continue
		}
		if t.op == "<(" {
			processSub = true
			add(t.op, false)
			continue
		}
		if member(t.op, "&&", "||", "|", ";", ">", ">>", "<") {
			add(t.op, false)
		}
	}
	result = jsTrimCommand(result)
	if result == "" {
		return original
	}
	return result
}

// Source's sandbox auto-allow precheck explicitly uses this legacy splitter;
// this named consumer does not attest AST safety for unsandboxed execution.
func (c *RecordedCommandClassifier) SplitForSandboxPermission(command string) ([]string, error) {
	return c.split(command)
}
