package sessionrewind

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
)

type heredoc struct {
	placeholder, full, delimiter                 string
	start, operatorEnd, contentStart, contentEnd int
}
type heredocExtraction struct {
	processed string
	items     []heredoc
}

func runeByteIndex(value string, index int) int {
	for i := range value {
		if index == 0 {
			return i
		}
		index--
	}
	return len(value)
}
func nativeHeredocs(command string, quotedOnly bool) (heredocExtraction, error) {
	result := heredocExtraction{processed: command}
	first := strings.Index(command, "<<")
	if first < 0 || jsMatch(`\$['"]`, command) || strings.Contains(command[:first], "`") || strings.Count(command[:first], "((") > strings.Count(command[:first], "))") {
		return result, nil
	}
	regex, _ := regexpNative(`(?<!<)<<(?!<)(-)?[ \t]*(?:(['"])(\\?\w+)\2|\\?(\w+))`)
	matches := []heredoc{}
	skipped := [][2]int{}
	scanPos := 0
	single, double, comment, dqEscape := false, false, false, false
	pendingBackslashes := 0
	scan := func(target int) {
		for i := scanPos; i < target; i++ {
			ch := command[i]
			if ch == '\n' {
				comment = false
			}
			if single {
				if ch == '\'' {
					single = false
				}
				continue
			}
			if double {
				if dqEscape {
					dqEscape = false
					continue
				}
				if ch == '\\' {
					dqEscape = true
					continue
				}
				if ch == '"' {
					double = false
				}
				continue
			}
			if ch == '\\' {
				pendingBackslashes++
				continue
			}
			escaped := pendingBackslashes%2 == 1
			pendingBackslashes = 0
			if escaped {
				continue
			}
			if ch == '\'' {
				single = true
			} else if ch == '"' {
				double = true
			} else if !comment && ch == '#' {
				comment = true
			}
		}
		scanPos = target
	}
	match, err := regex.FindStringMatch(command)
	if err != nil {
		return result, err
	}
	for match != nil {
		start := runeByteIndex(command, match.RuneIndex)
		end := start + len(match.String())
		scan(start)
		eligible := !single && !double && !comment && pendingBackslashes%2 == 0
		for _, span := range skipped {
			if start > span[0] && start < span[1] {
				eligible = false
			}
		}
		if eligible {
			dash := match.GroupByNumber(1).String() == "-"
			quote := match.GroupByNumber(2).String()
			delimiter := match.GroupByNumber(3).String()
			if delimiter == "" {
				delimiter = match.GroupByNumber(4).String()
			}
			isQuoted := quote != "" || strings.Contains(match.String(), "\\")
			if end < len(command) && !strings.ContainsRune(" \t\n|&;()<>", rune(command[end])) {
				eligible = false
			}
			if eligible {
				newline := -1
				sq, dq := false, false
				for i := end; i < len(command); i++ {
					ch := command[i]
					if sq {
						if ch == '\'' {
							sq = false
						}
						continue
					}
					if dq {
						if ch == '\\' {
							i++
							continue
						}
						if ch == '"' {
							dq = false
						}
						continue
					}
					if ch == '\n' {
						newline = i
						break
					}
					backslashes := 0
					for j := i - 1; j >= end && command[j] == '\\'; j-- {
						backslashes++
					}
					if backslashes%2 == 1 {
						continue
					}
					if ch == '\'' {
						sq = true
					} else if ch == '"' {
						dq = true
					}
				}
				if newline >= 0 {
					trailing := 0
					for j := newline - 1; j >= end && command[j] == '\\'; j-- {
						trailing++
					}
					if trailing%2 == 0 {
						lines := strings.Split(command[newline+1:], "\n")
						closing := -1
						for i, line := range lines {
							if dash {
								line = strings.TrimLeft(line, "\t")
							}
							if line == delimiter {
								closing = i
								break
							}
							if strings.HasPrefix(line, delimiter) && len(line) > len(delimiter) && strings.ContainsRune(")}`|&;(<>", rune(line[len(delimiter)])) {
								break
							}
						}
						contentEnd := len(command)
						if closing >= 0 {
							contentEnd = newline + 1 + len(strings.Join(lines[:closing+1], "\n"))
						}
						if quotedOnly && !isQuoted {
							skipped = append(skipped, [2]int{newline, contentEnd})
						} else if closing >= 0 {
							overlap := false
							for _, span := range skipped {
								if newline < span[1] && span[0] < contentEnd {
									overlap = true
								}
							}
							if !overlap {
								matches = append(matches, heredoc{full: command[start:end] + command[newline:contentEnd], delimiter: delimiter, start: start, operatorEnd: end, contentStart: newline, contentEnd: contentEnd})
							}
						}
					}
				}
			}
		}
		match, err = regex.FindNextMatch(match)
		if err != nil {
			return result, err
		}
	}
	top := []heredoc{}
	for i, candidate := range matches {
		nested := false
		for j, other := range matches {
			if i != j && candidate.start > other.contentStart && candidate.start < other.contentEnd {
				nested = true
				break
			}
		}
		if !nested {
			top = append(top, candidate)
		}
	}
	if len(top) == 0 {
		return result, nil
	}
	starts := map[int]bool{}
	for _, item := range top {
		if starts[item.contentStart] {
			return result, nil
		}
		starts[item.contentStart] = true
	}
	sort.SliceStable(top, func(a, b int) bool { return top[a].contentEnd > top[b].contentEnd })
	var salt [8]byte
	if _, err = rand.Read(salt[:]); err != nil {
		return result, err
	}
	suffix := hex.EncodeToString(salt[:])
	for i, item := range top {
		item.placeholder = "__HEREDOC_" + integerString(len(top)-1-i) + "_" + suffix + "__"
		result.items = append(result.items, item)
		result.processed = result.processed[:item.start] + item.placeholder + result.processed[item.operatorEnd:item.contentStart] + result.processed[item.contentEnd:]
	}
	return result, nil
}
func restoreNativeHeredocs(value string, items []heredoc) string {
	for _, item := range items {
		value = strings.ReplaceAll(value, item.placeholder, item.full)
	}
	return value
}
func (c *RecordedCommandClassifier) safeHeredocSubstitution(command string) (bool, error) {
	if !jsMatch(`\$\(.*<<`, command) {
		return false, nil
	}
	regex, _ := regexpNative(`\$\(cat[ \t]*<<(-?)[ \t]*(?:'+([A-Za-z_]\w*)'+|\\([A-Za-z_]\w*))`)
	verified := [][2]int{}
	match, err := regex.FindStringMatch(command)
	if err != nil {
		return false, err
	}
	for match != nil {
		start := runeByteIndex(command, match.RuneIndex)
		end := start + len(match.String())
		delimiter := match.GroupByNumber(2).String()
		if delimiter == "" {
			delimiter = match.GroupByNumber(3).String()
		}
		dash := match.GroupByNumber(1).String() == "-"
		after := command[end:]
		newline := strings.IndexByte(after, '\n')
		if newline < 0 || !jsMatch(`^[ \t]*$`, after[:newline]) {
			return false, nil
		}
		bodyStart := end + newline + 1
		lines := strings.Split(command[bodyStart:], "\n")
		closing, parenLine, parenColumn := -1, -1, 0
		for i, raw := range lines {
			line := raw
			if dash {
				line = strings.TrimLeft(line, "\t")
			}
			if line == delimiter {
				closing = i
				if i+1 >= len(lines) {
					return false, nil
				}
				next := lines[i+1]
				prefix := len(next) - len(strings.TrimLeft(next, " \t"))
				if prefix >= len(next) || next[prefix] != ')' {
					return false, nil
				}
				parenLine = i + 1
				parenColumn = prefix
				break
			}
			if strings.HasPrefix(line, delimiter) {
				tail := line[len(delimiter):]
				prefix := len(tail) - len(strings.TrimLeft(tail, " \t"))
				if prefix < len(tail) && tail[prefix] == ')' {
					closing = i
					parenLine = i
					tabs := 0
					if dash {
						tabs = len(raw) - len(strings.TrimLeft(raw, "\t"))
					}
					parenColumn = tabs + len(delimiter) + prefix
					break
				}
				if tail != "" && strings.ContainsRune(")}`|&;(<>", rune(tail[0])) {
					return false, nil
				}
			}
		}
		if closing < 0 {
			return false, nil
		}
		endPos := bodyStart
		for _, line := range lines[:parenLine] {
			endPos += len(line) + 1
		}
		endPos += parenColumn + 1
		verified = append(verified, [2]int{start, endPos})
		match, err = regex.FindNextMatch(match)
		if err != nil {
			return false, err
		}
	}
	if len(verified) == 0 {
		return false, nil
	}
	for i, outer := range verified {
		for j, inner := range verified {
			if i != j && inner[0] > outer[0] && inner[0] < outer[1] {
				return false, nil
			}
		}
	}
	sort.SliceStable(verified, func(a, b int) bool { return verified[a][0] > verified[b][0] })
	remaining := command
	first := len(command)
	for _, span := range verified {
		remaining = remaining[:span[0]] + remaining[span[1]:]
		if span[0] < first {
			first = span[0]
		}
	}
	if jsTrimCommand(remaining) != "" && jsTrimCommand(command[:first]) == "" {
		return false, nil
	}
	if !jsMatch(`^[a-zA-Z0-9 \t"'.\-/_@=,:+~]*$`, remaining) {
		return false, nil
	}
	return c.security(remaining)
}
func integerString(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
