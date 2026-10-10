package sessionrewind

import "strings"

type quotedContent struct{ double, fully, keep string }

func extractNativeQuotes(command string, jq bool) quotedContent {
	var double, fully, keep strings.Builder
	single, dq, escaped := false, false, false
	for _, r := range command {
		if escaped {
			escaped = false
			if !single {
				double.WriteRune(r)
			}
			if !single && !dq {
				fully.WriteRune(r)
				keep.WriteRune(r)
			}
			continue
		}
		if r == '\\' && !single {
			escaped = true
			double.WriteRune(r)
			if !dq {
				fully.WriteRune(r)
				keep.WriteRune(r)
			}
			continue
		}
		if r == '\'' && !dq {
			single = !single
			keep.WriteRune(r)
			continue
		}
		if r == '"' && !single {
			dq = !dq
			keep.WriteRune(r)
			if !jq {
				continue
			}
		}
		if !single {
			double.WriteRune(r)
		}
		if !single && !dq {
			fully.WriteRune(r)
			keep.WriteRune(r)
		}
	}
	return quotedContent{double.String(), fully.String(), keep.String()}
}
func shellQuoteBug(command string) bool {
	single, double := false, false
	for i := 0; i < len(command); i++ {
		r := command[i]
		if r == '\\' && !single {
			i++
			continue
		}
		if r == '"' && !single {
			double = !double
			continue
		}
		if r == '\'' && !double {
			single = !single
			if !single {
				count := 0
				for j := i - 1; j >= 0 && command[j] == '\\'; j-- {
					count++
				}
				if count > 0 && (count%2 == 1 || strings.Contains(command[i+1:], "'")) {
					return true
				}
			}
		}
	}
	return false
}
func hasUnescaped(content string, char byte) bool {
	for i := 0; i < len(content); i++ {
		if content[i] == '\\' && i+1 < len(content) {
			i++
			continue
		}
		if content[i] == char {
			return true
		}
	}
	return false
}
func (c *RecordedCommandClassifier) security(command string) (bool, error) {
	if jsMatch(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`, command) || shellQuoteBug(command) {
		return false, nil
	}
	heredocs, err := nativeHeredocs(command, true)
	if err != nil {
		return false, err
	}
	base := strings.Split(command, " ")[0]
	q := extractNativeQuotes(heredocs.processed, base == "jq")
	fully := q.fully
	// Source strips only these literal redirects before its remaining validators.
	for _, pattern := range []string{`\s+2\s*>&\s*1(?=\s|$)`, `[012]?\s*>\s*/dev/null(?=\s|$)`, `\s*<\s*/dev/null(?=\s|$)`} {
		compiled, _ := regexpNative(pattern)
		fully, _ = compiled.Replace(fully, "", -1, -1)
	}
	if jsMatch(`^\s*\t`, command) || strings.HasPrefix(jsTrimCommand(command), "-") || jsMatch(`^\s*(&&|\|\||;|>>?|<)`, command) {
		return false, nil
	}
	if safe, err := c.safeHeredocSubstitution(command); err != nil || safe {
		return safe, err
	}
	if handled, allowed := nativeGitCommit(command, base); handled {
		return allowed, nil
	}
	afterJq := ""
	if len(command) > 3 {
		afterJq = command[3:]
	}
	if base == "jq" && (jsMatch(`\bsystem\s*\(`, command) || jsMatch(`(?:^|\s)(?:-f\b|--from-file|--rawfile|--slurpfile|-L\b|--library-path)`, jsTrimCommand(afterJq))) {
		return false, nil
	}
	if base != "echo" || strings.ContainsAny(command, "|&;") {
		if bad, err := nativeObfuscated(command, base, q.fully); err != nil || bad {
			return false, err
		}
	}
	for _, pattern := range []string{`(?:^|\s)["'][^"']*[;&][^"']*["'](?:\s|$)`, `-name\s+["'][^"']*[;|&][^"']*["']`, `-path\s+["'][^"']*[;|&][^"']*["']`, `-iname\s+["'][^"']*[;|&][^"']*["']`, `-regex\s+["'][^"']*[;&][^"']*["']`} {
		if jsMatch(pattern, q.double) {
			return false, nil
		}
	}
	if jsMatch(`[<>|]\s*\$[A-Za-z_]`, fully) || jsMatch(`\$[A-Za-z_][A-Za-z0-9_]*\s*[|<>]`, fully) {
		return false, nil
	}
	if nativeCommentDesync(command) || nativeQuotedNewline(command) || nativeCR(command) {
		return false, nil
	}
	if jsMatch(`(?<![\s]\\)[\n\r]\s*\S`, q.fully) || jsMatch(`\$IFS|\$\{[^}]*IFS`, command) || jsMatch(`/proc/.*/environ`, command) {
		return false, nil
	}
	if hasUnescaped(q.double, '`') {
		return false, nil
	}
	for _, pattern := range []string{`<\(`, `>\(`, `=\(`, `(?:^|[\s;&|])=[a-zA-Z_]`, `\$\(`, `\$\{`, `\$\[`, `~\[`, `\(e:`, `\(\+`, `\}\s*always\s*\{`, `<#`} {
		if jsMatch(pattern, q.double) {
			return false, nil
		}
	}
	if strings.ContainsAny(fully, "<>") || nativeEscapedSpecial(command) || jsMatch(`[\u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000\uFEFF]`, command) {
		return false, nil
	}
	if jsMatch(`\S(?<!\$\{)#`, q.keep) || jsMatch(`\S(?<!\$\{)#`, joinContinuations(q.keep)) || nativeBrace(q.fully, command) {
		return false, nil
	}
	if nativeZshDangerous(command) {
		return false, nil
	}
	parsed, err := c.parse(command, false)
	if err == nil {
		separator := false
		for _, token := range parsed {
			if member(token.op, ";", "&&", "||") {
				separator = true
			}
		}
		if separator && nativeMalformed(command, parsed) {
			return false, nil
		}
	}
	return true, nil
}
