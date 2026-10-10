package skillcommands

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func description(v *markdownfrontmatter.Value, body, label string) (string, bool) {
	if v != nil {
		switch v.Kind {
		case "string":
			if t := trimJS(v.String()); t != "" {
				return t, true
			}
		case "number", "boolean":
			return v.String(), true
		}
	}
	for _, line := range strings.Split(body, "\n") {
		text := trimJS(line)
		if text == "" {
			continue
		}
		if m := regexp.MustCompile(`^#+\s+(.+)$`).FindStringSubmatch(text); m != nil {
			text = m[1]
		}
		u := utf16.Encode([]rune(text))
		if len(u) > 100 {
			text = string(utf16.Decode(u[:97])) + "..."
		}
		return text, false
	}
	return label, false
}
func rawStrings(raw json.RawMessage) []string {
	var out []string
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) == nil {
		for _, entry := range entries {
			if json.Unmarshal(entry, &single) == nil {
				out = append(out, single)
			}
		}
	}
	return out
}
func parseTools(raw json.RawMessage) []string {
	out := []string{}
	for _, value := range rawStrings(raw) {
		var current strings.Builder
		parens := false
		flush := func() {
			if s := trimJS(current.String()); s != "" {
				out = append(out, s)
			}
			current.Reset()
		}
		for _, r := range value {
			switch r {
			case '(':
				parens = true
				current.WriteRune(r)
			case ')':
				parens = false
				current.WriteRune(r)
			case ',':
				if parens {
					current.WriteRune(r)
				} else {
					flush()
				}
			case ' ':
				if parens {
					current.WriteRune(r)
				} else if trimJS(current.String()) != "" {
					flush()
				}
			default:
				current.WriteRune(r)
			}
		}
		flush()
	}
	return out
}

var digits = regexp.MustCompile(`^\d+$`)

func parseArgumentNames(raw json.RawMessage) []string {
	values := rawStrings(raw)
	if len(raw) > 0 && raw[0] == '"' && len(values) > 0 {
		values = regexp.MustCompile(`\s+`).Split(values[0], -1)
	}
	out := []string{}
	for _, name := range values {
		if trimJS(name) != "" && !digits.MatchString(name) {
			out = append(out, name)
		}
	}
	return out
}
func parseEffort(v *markdownfrontmatter.Value) json.RawMessage {
	if v == nil || v.Kind == "null" || v.Kind == "string" && v.String() == "" {
		return nil
	}
	s := strings.ToLower(trimJS(v.String()))
	for _, choice := range []string{"low", "medium", "high", "xhigh", "max"} {
		if s == choice {
			raw, _ := json.Marshal(s)
			return raw
		}
	}
	if !regexp.MustCompile(`^-?\d+$`).MatchString(s) && v.Kind != "number" {
		return nil
	}
	number, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsNaN(number) || math.IsInf(number, 0) || number != math.Trunc(number) {
		return nil
	}
	if number == 0 {
		return json.RawMessage("0")
	}
	return protocol.StringifyValidatedJavaScriptJSON([]byte(strconv.FormatFloat(number, 'g', -1, 64)))
}

type expansionBudget struct {
	results, bytes int
	exhausted      bool
}

func parsePaths(raw json.RawMessage) []string {
	values := rawStrings(raw)
	budget := &expansionBudget{}
	out := []string{}
	for _, value := range values {
		for _, part := range splitCommas(value) {
			for _, path := range expand(part, budget) {
				path = strings.TrimSuffix(path, "/**")
				if path != "" {
					out = append(out, path)
				}
			}
		}
	}
	all := len(out) == 0
	if len(out) > 0 {
		all = true
		for _, p := range out {
			if p != "**" {
				all = false
			}
		}
	}
	if all {
		return nil
	}
	return out
}
func splitCommas(s string) []string {
	out := []string{}
	depth, start := 0, 0
	for at, r := range s {
		if r == '{' {
			depth++
		}
		if r == '}' {
			depth--
		}
		if r == ',' && depth == 0 {
			if p := trimJS(s[start:at]); p != "" {
				out = append(out, p)
			}
			start = at + 1
		}
	}
	if p := trimJS(s[start:]); p != "" {
		out = append(out, p)
	}
	return out
}

var braces = regexp.MustCompile(`^([^{]*)\{([^}]+)\}(.*)$`)

func expand(pattern string, b *expansionBudget) []string {
	if b.exhausted {
		return []string{pattern}
	}
	pending := []string{pattern}
	out := []string{}
	for len(pending) > 0 {
		s := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		b.bytes += len(utf16.Encode([]rune(s)))
		m := braces.FindStringSubmatch(s)
		if m == nil {
			out = append(out, s)
			continue
		}
		parts := strings.Split(m[2], ",")
		projected := b.results + len(out) + len(pending) + len(parts)
		if b.bytes > 4*1024*1024 || projected > 1000 || projected*len(utf16.Encode([]rune(pattern))) > 4*1024*1024-b.bytes {
			b.exhausted = true
			return []string{pattern}
		}
		for i := len(parts) - 1; i >= 0; i-- {
			pending = append(pending, m[1]+trimJS(parts[i])+m[3])
		}
	}
	b.results += len(out)
	return out
}

var configVariable = regexp.MustCompile(`\$\{user_config\.([^}]+)\}`)

func substituteUserConfig(text string, values, schema protocol.JSONObject) string {
	return configVariable.ReplaceAllStringFunc(text, func(match string) string {
		key := configVariable.FindStringSubmatch(match)[1]
		var declaration protocol.JSONObject
		if declaration.UnmarshalJSON(schema.Get(key)) == nil && string(declaration.Get("sensitive")) == "true" {
			return "[sensitive option '" + key + "' not available in skill content]"
		}
		raw := values.Get(key)
		if raw == nil {
			return match
		}
		value, e := protocol.JavaScriptStringJSON(raw)
		if e != nil {
			return match
		}
		var s string
		_ = json.Unmarshal(value, &s)
		return s
	})
}
