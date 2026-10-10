package skillcommands

import (
	"strings"

	"github.com/dlclark/regexp2/v2"
)

type PromptShellMatch struct{ Pattern, Command string }

// Preserve the two matchAll passes and their order. Inline matches nested in
// a block are intentionally retained, just as in the original Promise.all.
func PromptShellMatches(text string) ([]PromptShellMatch, error) {
	patterns := []*regexp2.Regexp{regexp2.MustCompile("```!\\s*\\n?([\\s\\S]*?)\\n?```", regexp2.ECMAScript)}
	if strings.Contains(text, "!`") {
		patterns = append(patterns, regexp2.MustCompile("(?<=^|\\s)!`([^`]+)`", regexp2.ECMAScript|regexp2.Multiline))
	}
	out := []PromptShellMatch{}
	for _, pattern := range patterns {
		match, e := pattern.FindStringMatch(text)
		for match != nil && e == nil {
			command := trimJS(match.Groups()[1].String())
			if command != "" {
				out = append(out, PromptShellMatch{match.String(), command})
			}
			match, e = pattern.FindNextMatch(match)
		}
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
