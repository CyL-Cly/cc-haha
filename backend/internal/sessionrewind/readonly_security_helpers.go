package sessionrewind

import (
	"github.com/dlclark/regexp2/v2"
	"strings"
)

func regexpNative(pattern string) (*regexp2.Regexp, error) {
	return regexp2.Compile(pattern, regexp2.ECMAScript)
}
func nativeCR(command string) bool {
	single, double, escaped := false, false, false
	for _, r := range command {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && !single {
			escaped = true
			continue
		}
		if r == '\'' && !double {
			single = !single
			continue
		}
		if r == '"' && !single {
			double = !double
			continue
		}
		if r == '\r' && !double {
			return true
		}
	}
	return false
}
func nativeEscapedSpecial(command string) bool {
	single, double := false, false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if ch == '\\' && !single {
			if !double && i+1 < len(command) && strings.ContainsRune(" \t;|&<>", rune(command[i+1])) {
				return true
			}
			i++
			continue
		}
		if ch == '\'' && !double {
			single = !single
		}
		if ch == '"' && !single {
			double = !double
		}
	}
	return false
}
func nativeCommentDesync(command string) bool {
	single, double, escaped := false, false, false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if single {
			if ch == '\'' {
				single = false
			}
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if double {
			if ch == '"' {
				double = false
			}
			continue
		}
		if ch == '\'' {
			single = true
			continue
		}
		if ch == '"' {
			double = true
			continue
		}
		if ch == '#' {
			end := strings.IndexByte(command[i:], '\n')
			if end < 0 {
				end = len(command) - i
			}
			comment := command[i+1 : i+end]
			if strings.ContainsAny(comment, `'"`) {
				return true
			}
			if i+end == len(command) {
				break
			}
			i += end
		}
	}
	return false
}
func nativeQuotedNewline(command string) bool {
	if !strings.Contains(command, "\n") || !strings.Contains(command, "#") {
		return false
	}
	single, double, escaped := false, false, false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !single {
			escaped = true
			continue
		}
		if ch == '\'' && !double {
			single = !single
			continue
		}
		if ch == '"' && !single {
			double = !double
			continue
		}
		if ch == '\n' && (single || double) {
			next := command[i+1:]
			if end := strings.IndexByte(next, '\n'); end >= 0 {
				next = next[:end]
			}
			if strings.HasPrefix(jsTrimCommand(next), "#") {
				return true
			}
		}
	}
	return false
}
func escapedPosition(value string, i int) bool {
	count := 0
	for i--; i >= 0 && value[i] == '\\'; i-- {
		count++
	}
	return count%2 == 1
}
func nativeBrace(content, original string) bool {
	open, close := 0, 0
	for i := range content {
		if !escapedPosition(content, i) {
			if content[i] == '{' {
				open++
			}
			if content[i] == '}' {
				close++
			}
		}
	}
	if open > 0 && close > open {
		return true
	}
	if open > 0 && jsMatch(`['"][{}]['"]`, original) {
		return true
	}
	for i := range content {
		if content[i] != '{' || escapedPosition(content, i) {
			continue
		}
		depth, end := 1, -1
		for j := i + 1; j < len(content); j++ {
			if escapedPosition(content, j) {
				continue
			}
			if content[j] == '{' {
				depth++
			}
			if content[j] == '}' {
				depth--
				if depth == 0 {
					end = j
					break
				}
			}
		}
		if end < 0 {
			continue
		}
		depth = 0
		for j := i + 1; j < end; j++ {
			if escapedPosition(content, j) {
				continue
			}
			if content[j] == '{' {
				depth++
			} else if content[j] == '}' {
				depth--
			} else if depth == 0 && (content[j] == ',' || (content[j] == '.' && j+1 < end && content[j+1] == '.')) {
				return true
			}
		}
	}
	return false
}
func nativeMalformed(command string, tokens []shellToken) bool {
	single, double, singleCount, doubleCount := false, false, 0, 0
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if ch == '\\' && !single {
			i++
			continue
		}
		if ch == '"' && !single {
			doubleCount++
			double = !double
		} else if ch == '\'' && !double {
			singleCount++
			single = !single
		}
	}
	if singleCount%2 != 0 || doubleCount%2 != 0 {
		return true
	}
	for _, token := range tokens {
		if !token.isString {
			continue
		}
		for _, pair := range [][2]string{{"{", "}"}, {"(", ")"}, {"[", "]"}} {
			if strings.Count(token.text, pair[0]) != strings.Count(token.text, pair[1]) {
				return true
			}
		}
		for _, quote := range []byte{'\'', '"'} {
			count := 0
			for i := range token.text {
				if token.text[i] == quote && (i == 0 || token.text[i-1] != '\\') {
					count++
				}
			}
			if count%2 != 0 {
				return true
			}
		}
	}
	return false
}
func nativeObfuscated(command, base, fully string) (bool, error) {
	for _, pattern := range []string{`\$'[^']*'`, `\$"[^"]*"`, `\$['"]{2}\s*-`, `(?:^|\s)(?:''|"")+\s*-`, `(?:""|'')+['"]-`, `(?:^|\s)['"]{3,}`} {
		if jsMatch(pattern, command) {
			return true, nil
		}
	}
	single, double, escaped := false, false, false
	for i := 0; i < len(command)-1; i++ {
		ch, next := command[i], command[i+1]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !single {
			escaped = true
			continue
		}
		if ch == '\'' && !double {
			single = !single
			continue
		}
		if ch == '"' && !single {
			double = !double
			continue
		}
		if single || double {
			continue
		}
		if jsMatch(`\s`, string(ch)) && strings.ContainsRune("'\"`", rune(next)) {
			quote := next
			j := i + 2
			for j < len(command) && command[j] != quote {
				j++
			}
			inside := command[i+2 : j]
			after := byte(0)
			if j+1 < len(command) {
				after = command[j+1]
			}
			bad := jsMatch("^-+[a-zA-Z0-9$`]", inside) || (jsMatch(`^-+$`, inside) && after != 0 && strings.ContainsAny(string(after), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789\\${`-"))
			if (inside == "" || jsMatch(`^-+$`, inside)) && after != 0 && strings.ContainsRune("'\"`", rune(after)) {
				pos, combined := j+1, inside
				for pos < len(command) && strings.ContainsRune("'\"`", rune(command[pos])) {
					q := command[pos]
					end := pos + 1
					for end < len(command) && command[end] != q {
						end++
					}
					segment := command[pos+1 : end]
					combined += segment
					if jsMatch("^-+[a-zA-Z0-9$`]", combined) {
						bad = true
						break
					}
					prior := combined
					if len(segment) > 0 {
						prior = combined[:len(combined)-len(segment)]
					}
					if jsMatch(`^-+$`, prior) && jsMatch("[a-zA-Z0-9$`]", segment) {
						bad = true
						break
					}
					if end >= len(command) {
						break
					}
					pos = end + 1
				}
				if pos < len(command) && jsMatch("[a-zA-Z0-9\\\\${`-]", string(command[pos])) {
					if jsMatch(`^-+$`, combined) || combined == "" {
						if command[pos] == '-' || (combined != "" && jsMatch("[a-zA-Z0-9\\\\${`]", string(command[pos]))) {
							bad = true
						}
					}
					if strings.HasPrefix(combined, "-") {
						bad = true
					}
				}
			}
			if j < len(command) && command[j] == quote && bad {
				return true, nil
			}
		}
		if jsMatch(`\s`, string(ch)) && next == '-' {
			j := i + 1
			flag := ""
			for j < len(command) {
				r := command[j]
				if jsMatch(`[\s=]`, string(r)) {
					break
				}
				if strings.ContainsRune("'\"`", rune(r)) {
					if base == "cut" && flag == "-d" {
						break
					}
					if j+1 < len(command) && !jsMatch(`[a-zA-Z0-9_'"-]`, string(command[j+1])) {
						break
					}
				}
				flag += string(r)
				j++
			}
			if strings.ContainsAny(flag, `'"`) {
				return true, nil
			}
		}
	}
	return jsMatch("\\s['\"`]-", fully) || jsMatch("['\"`]{2}-", fully), nil
}
