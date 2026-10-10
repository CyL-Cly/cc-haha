package sessionrewind

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

func joinContinuations(command string) string {
	var result strings.Builder
	for i := 0; i < len(command); {
		if command[i] != '\\' {
			result.WriteByte(command[i])
			i++
			continue
		}
		j := i
		for j < len(command) && command[j] == '\\' {
			j++
		}
		if j < len(command) && command[j] == '\n' && (j-i)%2 == 1 {
			result.WriteString(command[i : j-1])
			i = j + 1
		} else {
			result.WriteString(command[i:j])
			i = j
		}
	}
	return result.String()
}
func staticRedirect(path string) bool {
	return path != "" && !jsMatch(`[\s'"]`, path) && !strings.HasPrefix(path, "#") && !strings.HasPrefix(path, "!") && !strings.HasPrefix(path, "=") && !strings.HasPrefix(path, "&") && !strings.ContainsAny(path, "$`*?[{~(<")
}
func (c *RecordedCommandClassifier) splitWithOperators(command string) ([]string, error) {
	heredocs, err := nativeHeredocs(command, false)
	if err != nil {
		return nil, err
	}
	var salt [8]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return nil, err
	}
	suffix := hex.EncodeToString(salt[:])
	single, double, newline, open, close := "__SINGLE_QUOTE_"+suffix+"__", "__DOUBLE_QUOTE_"+suffix+"__", "__NEW_LINE_"+suffix+"__", "__ESCAPED_OPEN_PAREN_"+suffix+"__", "__ESCAPED_CLOSE_PAREN_"+suffix+"__"
	joined := joinContinuations(heredocs.processed)
	processed := strings.NewReplacer(`"`, `"`+double, `'`, `'`+single, "\n", "\n"+newline+"\n", `\(`, open, `\)`, close).Replace(joined)
	tokens, err := c.parse(processed, true)
	if err != nil {
		return []string{joinContinuations(command)}, nil
	}
	parts := []*shellToken{}
	for _, token := range tokens {
		token := token
		if token.isString && len(parts) > 0 && parts[len(parts)-1] != nil && parts[len(parts)-1].isString {
			if token.text == newline {
				parts = append(parts, nil)
			} else {
				parts[len(parts)-1].text += " " + token.text
			}
			continue
		}
		if token.op == "glob" && len(parts) > 0 && parts[len(parts)-1] != nil && parts[len(parts)-1].isString {
			parts[len(parts)-1].text += " " + token.pattern
			continue
		}
		parts = append(parts, &token)
	}
	result := []*string{}
	restore := strings.NewReplacer(single, "'", double, `"`, "\n"+newline+"\n", "\n", open, `\(`, close, `\)`)
	for _, part := range parts {
		if part == nil {
			continue
		}
		value := ""
		switch {
		case part.isString:
			value = part.text
		case part.isComment:
			value = "#" + strings.NewReplacer(`"`+double, double, `'`+single, single).Replace(part.comment)
		case part.op == "glob":
			value = part.pattern
		case part.op != "":
			value = part.op
		default:
			continue
		}
		value = restoreNativeHeredocs(restore.Replace(value), heredocs.items)
		result = append(result, &value)
	}
	values := make([]string, 0, len(result))
	for _, value := range result {
		values = append(values, *value)
	}
	return values, nil
}

// SplitWithOperators shares Source splitCommandWithOperators, preserving
// redirects and separators. It is display metadata, never a permission gate.
func (c *RecordedCommandClassifier) SplitWithOperators(command string) ([]string, error) {
	return c.splitWithOperators(command)
}

func (c *RecordedCommandClassifier) split(command string) ([]string, error) {
	values, err := c.splitWithOperators(command)
	if err != nil {
		return nil, err
	}
	result := make([]*string, 0, len(values))
	for _, value := range values {
		value := value
		result = append(result, &value)
	}
	at := func(i int) string {
		if i < 0 || i >= len(result) || result[i] == nil {
			return ""
		}
		return jsTrimCommand(*result[i])
	}
	fd := func(value string) bool { return value == "0" || value == "1" || value == "2" }
	for i := range result {
		operator := at(i)
		if operator != ">&" && operator != ">" && operator != ">>" {
			continue
		}
		next, after, prev := at(i+1), at(i+2), at(i-1)
		if i+1 >= len(result) || result[i+1] == nil {
			continue
		}
		effective := next
		if (operator == ">" || operator == ">>") && len(next) >= 3 && next[len(next)-2] == ' ' && fd(next[len(next)-1:]) && member(after, ">", ">>", ">&") {
			effective = next[:len(next)-2]
		}
		strip, third := false, false
		if operator == ">&" && fd(next) {
			strip = true
		} else if operator == ">" && next == "&" && fd(after) {
			strip = true
			third = true
		} else if operator == ">" && strings.HasPrefix(next, "&") && fd(next[1:]) {
			strip = true
		} else if (operator == ">" || operator == ">>") && staticRedirect(effective) {
			strip = true
		}
		if strip {
			if len(prev) >= 3 && prev[len(prev)-2] == ' ' && fd(prev[len(prev)-1:]) {
				value := prev[:len(prev)-2]
				result[i-1] = &value
			}
			result[i] = nil
			result[i+1] = nil
			if third {
				result[i+2] = nil
			}
		}
	}
	commands := []string{}
	for _, part := range result {
		if part != nil && *part != "" && !member(*part, "&&", "||", ";", ";;", "|", ">&", ">", ">>") {
			commands = append(commands, *part)
		}
	}
	return commands, nil
}

// SplitForExitCode shares the original deprecated command splitter. It is a
// display/result heuristic; it must never be used as a security decision.
func (c *RecordedCommandClassifier) SplitForExitCode(command string) ([]string, error) {
	return c.split(command)
}
