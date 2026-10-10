package skillcommands

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dlclark/regexp2/v2"
)

// ParseArguments follows shell-quote's chunking and string-token filter. It
// does not execute shell code, expand environment variables, or expand globs.
func ParseArguments(args string) []string {
	if trimJS(args) == "" {
		return []string{}
	}
	chunker := regexp2.MustCompile(`(\|\||&&|;;|\|&|<\(|<<<|>>|>&|<&|[&;()|<>])|((\\['"|&;()<> \t]|[^\s'"|&;()<> \t])+|"((\\"|[^"])*?)"|'((\\'|[^'])*?)')+`, regexp2.ECMAScript)
	out := []string{}
	commented := false
	match, _ := chunker.FindStringMatch(args)
	for match != nil && !commented {
		s := match.String()
		if len(match.Groups()[1].Captures) > 0 {
			match, _ = chunker.FindNextMatch(match)
			continue
		}
		var value strings.Builder
		quote := byte(0)
		escape, glob := false, false
		for i := 0; i < len(s); i++ {
			c := s[i]
			if quote == 0 && (c == '*' || c == '?') {
				glob = true
			}
			switch {
			case escape:
				value.WriteByte(c)
				escape = false
			case quote != 0:
				if c == quote {
					quote = 0
				} else if quote == '\'' {
					value.WriteByte(c)
				} else if c == '\\' {
					i++
					if i < len(s) {
						if s[i] != '"' && s[i] != '\\' && s[i] != '$' {
							value.WriteByte('\\')
						}
						value.WriteByte(s[i])
					}
				} else if c == '$' {
					v, end, e := parseVariable(s, i)
					if e != nil {
						return strings.Fields(args)
					}
					value.WriteString(v)
					i = end
				} else {
					value.WriteByte(c)
				}
			case c == '"' || c == '\'':
				quote = c
			case c == '#':
				commented = true
				i = len(s)
			case c == '\\':
				escape = true
			case c == '$':
				v, end, e := parseVariable(s, i)
				if e != nil {
					return strings.Fields(args)
				}
				value.WriteString(v)
				i = end
			default:
				value.WriteByte(c)
			}
		}
		if !glob && (!commented || value.Len() > 0) {
			out = append(out, value.String())
		}
		match, _ = chunker.FindNextMatch(match)
	}
	return out
}
func parseVariable(s string, i int) (string, int, error) {
	i++
	if i < len(s) && s[i] == '{' {
		start := i + 1
		end := strings.IndexByte(s[start:], '}')
		if end < 0 || end == 0 {
			return "", i, fmt.Errorf("Bad substitution")
		}
		end += start
		return "$" + s[start:end], end, nil
	}
	if i < len(s) && strings.ContainsRune("*@#?$!_-", rune(s[i])) {
		return "$" + s[i:i+1], i + 1, nil
	}
	start := i
	for i < len(s) {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			break
		}
		i++
	}
	return "$" + s[start:i], i - 1, nil
}

var indexed = regexp.MustCompile(`\$ARGUMENTS\[(\d+)\]`)

func SubstituteArguments(content string, args *string, names []string) (string, error) {
	if args == nil {
		return content, nil
	}
	parsed := ParseArguments(*args)
	original := content
	for at, name := range names {
		if name == "" {
			continue
		}
		r, e := regexp2.Compile(`\$`+name+`(?![\[\w])`, regexp2.ECMAScript)
		if e != nil {
			return "", e
		}
		value := ""
		if at < len(parsed) {
			value = parsed[at]
		}
		content, e = replaceRegex(content, r, func(match, prefix, suffix string) string { return jsReplacement(value, match, prefix, suffix) })
		if e != nil {
			return "", e
		}
	}
	content = indexed.ReplaceAllStringFunc(content, func(match string) string {
		n, e := strconv.Atoi(indexed.FindStringSubmatch(match)[1])
		if e != nil || n >= len(parsed) {
			return ""
		}
		return parsed[n]
	})
	shorthand := regexp2.MustCompile(`\$(\d+)(?!\w)`, regexp2.ECMAScript)
	var e error
	content, e = replaceRegex(content, shorthand, func(match, _, _ string) string {
		n, _ := strconv.Atoi(match[1:])
		if n < 0 || n >= len(parsed) {
			return ""
		}
		return parsed[n]
	})
	if e != nil {
		return "", e
	}
	text := content
	content = ""
	start := 0
	for {
		at := strings.Index(text[start:], "$ARGUMENTS")
		if at < 0 {
			content += text[start:]
			break
		}
		at += start
		content += text[start:at] + jsReplacement(*args, "$ARGUMENTS", text[:at], text[at+10:])
		start = at + 10
	}
	if content == original && *args != "" {
		content += "\n\nARGUMENTS: " + *args
	}
	return content, nil
}
func replaceRegex(text string, r *regexp2.Regexp, fn func(string, string, string) string) (string, error) {
	// regexp2 indexes Unicode runes. Convert offsets before slicing UTF-8.
	runes := []rune(text)
	out := strings.Builder{}
	end := 0
	m, e := r.FindStringMatch(text)
	for m != nil && e == nil {
		out.WriteString(string(runes[end:m.RuneIndex]))
		out.WriteString(fn(m.String(), string(runes[:m.RuneIndex]), string(runes[m.RuneIndex+m.RuneLength:])))
		end = m.RuneIndex + m.RuneLength
		m, e = r.FindNextMatch(m)
	}
	out.WriteString(string(runes[end:]))
	return out.String(), e
}
func jsReplacement(value, match, prefix, suffix string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '$' && i+1 < len(value) {
			switch value[i+1] {
			case '$':
				out.WriteByte('$')
				i++
				continue
			case '&':
				out.WriteString(match)
				i++
				continue
			case '`':
				out.WriteString(prefix)
				i++
				continue
			case '\'':
				out.WriteString(suffix)
				i++
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		out.WriteRune(r)
		i += size - 1
	}
	return out.String()
}
