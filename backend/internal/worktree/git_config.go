package worktree

import "strings"

func configSection(line, section string, subsection *string) bool {
	i := 1
	for i < len(line) && line[i] != ']' && line[i] != ' ' && line[i] != '\t' && line[i] != '"' {
		i++
	}
	if strings.ToLower(line[1:i]) != strings.ToLower(section) {
		return false
	}
	if subsection == nil {
		return i < len(line) && line[i] == ']'
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i == len(line) || line[i] != '"' {
		return false
	}
	i++
	var found strings.Builder
	for i < len(line) && line[i] != '"' {
		if line[i] == '\\' && i+1 < len(line) {
			i++
			found.WriteByte(line[i])
			i++
			continue
		}
		found.WriteByte(line[i])
		i++
	}
	return i+1 < len(line) && line[i] == '"' && line[i+1] == ']' && found.String() == *subsection
}
func ParseConfigValue(config, section string, subsection *string, key string) *string {
	active := false
	for _, line := range strings.Split(config, "\n") {
		line = trim(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			active = configSection(line, section, subsection)
			continue
		}
		if !active {
			continue
		}
		i := 0
		for i < len(line) && (line[i] >= 'a' && line[i] <= 'z' || line[i] >= 'A' && line[i] <= 'Z' || line[i] >= '0' && line[i] <= '9' || line[i] == '-') {
			i++
		}
		if i == 0 {
			continue
		}
		found := line[:i]
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i == len(line) || line[i] != '=' {
			continue
		}
		i++
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if !strings.EqualFold(found, key) {
			continue
		}
		var value strings.Builder
		quote := false
		for i < len(line) {
			ch := line[i]
			if !quote && (ch == '#' || ch == ';') {
				break
			}
			if ch == '"' {
				quote = !quote
				i++
				continue
			}
			if ch == '\\' && i+1 < len(line) {
				next := line[i+1]
				if quote {
					switch next {
					case 'n':
						value.WriteByte('\n')
					case 't':
						value.WriteByte('\t')
					case 'b':
						value.WriteByte('\b')
					default:
						value.WriteByte(next)
					}
					i += 2
					continue
				}
				if next == '\\' {
					value.WriteByte('\\')
					i += 2
					continue
				}
			}
			value.WriteByte(ch)
			i++
		}
		out := value.String()
		if !quote {
			out = strings.TrimRight(out, " \t")
		}
		return &out
	}
	return nil
}
