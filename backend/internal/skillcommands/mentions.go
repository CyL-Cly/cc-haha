package skillcommands

import (
	"strconv"
	"strings"

	"github.com/dlclark/regexp2/v2"
)

type MentionedFile struct {
	Filename  string   `json:"filename"`
	LineStart *float64 `json:"lineStart,omitempty"`
	LineEnd   *float64 `json:"lineEnd,omitempty"`
}

type PromptMentions struct {
	Files     []string        `json:"files"`
	Resources []string        `json:"resources"`
	Agents    []string        `json:"agents"`
	Parsed    []MentionedFile `json:"parsed"`
}

func uniqueMentions(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func mentionMatches(text, expression string) ([]*regexp2.Match, error) {
	// regexp2's boundary shorthand still treats Unicode letters as words in
	// ECMAScript mode. These Source regexes have no /u or /i flag, so JS \w,
	// \d and \b use ASCII word characters and decimal digits.
	expression = strings.ReplaceAll(expression, `\w`, `A-Za-z0-9_`)
	expression = strings.ReplaceAll(expression, `\b`, `(?:(?<=[A-Za-z0-9_])(?![A-Za-z0-9_])|(?<![A-Za-z0-9_])(?=[A-Za-z0-9_]))`)
	pattern := regexp2.MustCompile(expression, regexp2.ECMAScript)
	out := []*regexp2.Match{}
	match, e := pattern.FindStringMatch(text)
	for match != nil && e == nil {
		out = append(out, match)
		match, e = pattern.FindNextMatch(match)
	}
	return out, e
}

// ExtractPromptMentions preserves the original independent regex passes. An
// email address is not a mention; resources and legacy agents also remain in
// the file pass, where the real filesystem producer independently handles it.
func ExtractPromptMentions(text string) (PromptMentions, error) {
	out := PromptMentions{Files: []string{}, Resources: []string{}, Agents: []string{}, Parsed: []MentionedFile{}}
	quoted, e := mentionMatches(text, `(^|\s)@"([^"]+)"`)
	if e != nil {
		return out, e
	}
	for _, match := range quoted {
		value := match.Groups()[2].String()
		if value != "" && !strings.HasSuffix(value, " (agent)") {
			out.Files = append(out.Files, value)
		}
	}
	regular, e := mentionMatches(text, `(^|\s)@([^\s]+)\b`)
	if e != nil {
		return out, e
	}
	for _, match := range regular {
		value := match.String()
		value = value[strings.Index(value, "@")+1:]
		if !strings.HasPrefix(value, `"`) {
			out.Files = append(out.Files, value)
		}
	}
	out.Files = uniqueMentions(out.Files)
	resource, e := mentionMatches(text, `(^|\s)@([^\s]+:[^\s]+)\b`)
	if e != nil {
		return out, e
	}
	for _, match := range resource {
		value := match.String()
		out.Resources = append(out.Resources, value[strings.Index(value, "@")+1:])
	}
	out.Resources = uniqueMentions(out.Resources)
	agents, e := mentionMatches(text, `(^|\s)@"([\w:.@-]+) \(agent\)"`)
	if e != nil {
		return out, e
	}
	for _, match := range agents {
		out.Agents = append(out.Agents, match.Groups()[2].String())
	}
	legacy, e := mentionMatches(text, `(^|\s)@(agent-[\w:.@-]+)`)
	if e != nil {
		return out, e
	}
	for _, match := range legacy {
		value := match.String()
		out.Agents = append(out.Agents, value[strings.Index(value, "@")+1:])
	}
	out.Agents = uniqueMentions(out.Agents)
	for _, file := range out.Files {
		parsed, e := ParseMentionedFile(file)
		if e != nil {
			return out, e
		}
		out.Parsed = append(out.Parsed, parsed)
	}
	return out, nil
}

func ParseMentionedFile(mention string) (MentionedFile, error) {
	out := MentionedFile{Filename: mention}
	pattern := regexp2.MustCompile(`^([^#]+)(?:#L([0-9]+)(?:-([0-9]+))?)?(?:#[^#]*)?$`, regexp2.ECMAScript)
	match, e := pattern.FindStringMatch(mention)
	if e != nil || match == nil {
		return out, e
	}
	out.Filename = match.Groups()[1].String()
	if value := match.Groups()[2].String(); value != "" {
		start, _ := strconv.ParseFloat(value, 64)
		out.LineStart = &start
		out.LineEnd = &start
		if value := match.Groups()[3].String(); value != "" {
			end, _ := strconv.ParseFloat(value, 64)
			out.LineEnd = &end
		}
	}
	return out, nil
}
