package powershellpolicy

import (
	"regexp"
	"strings"
	"unicode"
)

type Rule struct {
	Source       string    `json:"source"`
	RuleBehavior string    `json:"ruleBehavior"`
	RuleValue    RuleValue `json:"ruleValue"`
}
type RuleValue struct {
	ToolName    string `json:"toolName"`
	RuleContent string `json:"ruleContent"`
}
type ShellRule struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Prefix  string `json:"prefix,omitempty"`
	Pattern string `json:"pattern,omitempty"`
}

func ParseRule(content string) ShellRule {
	if strings.HasSuffix(content, ":*") && len(content) > 2 && !strings.ContainsAny(strings.TrimSuffix(content, ":*"), "\r\n") {
		return ShellRule{Type: "prefix", Prefix: strings.TrimSuffix(content, ":*")}
	}
	if !strings.HasSuffix(content, ":*") {
		for i, r := range content {
			if r != '*' {
				continue
			}
			backslashes := 0
			for j := i - 1; j >= 0 && content[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 == 0 {
				return ShellRule{Type: "wildcard", Pattern: content}
			}
		}
	}
	return ShellRule{Type: "exact", Command: content}
}
func MatchWildcard(pattern, command string) bool {
	// Source's RegExp is /is without /u. ECMAScript Canonicalize refuses a
	// non-ASCII character whose uppercase would become ASCII (ſ→S), unlike
	// Go regexp IgnoreCase's Unicode SimpleFold (also K→K).
	pattern = ecmaRegexFold(jsTrim(pattern))
	command = ecmaRegexFold(command)
	var regex strings.Builder
	stars := 0
	characters := []rune(pattern)
	for i := 0; i < len(characters); i++ {
		ch := characters[i]
		if ch == '\\' && i+1 < len(characters) && (characters[i+1] == '*' || characters[i+1] == '\\') {
			i++
			regex.WriteString(regexp.QuoteMeta(string(characters[i])))
			continue
		}
		if ch == '*' {
			stars++
			regex.WriteString(".*")
		} else {
			regex.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	text := regex.String()
	if stars == 1 && strings.HasSuffix(text, " .*") {
		text = strings.TrimSuffix(text, " .*") + "( .*)?"
	}
	r, err := regexp.Compile("(?s)^" + text + "$")
	return err == nil && r.MatchString(command)
}
func ecmaRegexFold(text string) string {
	return strings.Map(func(r rune) rune {
		// Non-/u JS regex compares UTF-16 units. Supplementary-plane casing
		// cannot cross the surrogate pair as Go's rune case mapping would.
		if r > 0xffff {
			return r
		}
		upper := unicode.ToUpper(r)
		if r >= 128 && upper < 128 {
			return r
		}
		return upper
	}, text)
}
func commandHead(command string) (string, string) {
	command = jsTrim(command)
	first := command
	if i := strings.IndexFunc(command, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\v' || r == '\f' || r == 0xA0 || r == 0xFEFF
	}); i >= 0 {
		first = command[:i]
	}
	rest := command[len(first):]
	if rest != "" {
		rest = " " + strings.TrimLeftFunc(rest, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\v' || r == '\f' || r == 0xA0 || r == 0xFEFF
		})
	}
	return first, rest
}
func MatchesRule(command, content, matchMode, behavior string) bool {
	command = jsTrim(command)
	rawName, rest := commandHead(command)
	canonical := ResolveCanonical(StripModulePrefix(rawName))
	canonicalCommand := canonical + rest
	rule := ParseRule(content)
	match := func(value string) bool {
		switch rule.Type {
		case "exact":
			return jsEqual(rule.Command, value)
		case "prefix":
			return jsEqual(rule.Prefix, value) || (matchMode == "prefix" && strings.HasPrefix(jsLower(value), jsLower(rule.Prefix)+" "))
		case "wildcard":
			return matchMode != "exact" && MatchWildcard(rule.Pattern, value)
		}
		return false
	}
	if match(command) || match(canonicalCommand) {
		return true
	}
	ruleText := rule.Command
	if rule.Type == "prefix" {
		ruleText = rule.Prefix
	} else if rule.Type == "wildcard" {
		ruleText = rule.Pattern
	}
	ruleName, ruleRest := commandHead(ruleText)
	if behavior != "allow" {
		ruleName = StripModulePrefix(ruleName)
	}
	if ResolveCanonical(ruleName) != canonical {
		return false
	}
	switch rule.Type {
	case "exact":
		return jsEqual(ruleRest, rest)
	case "prefix":
		prefix := canonical + ruleRest
		return jsEqual(prefix, canonicalCommand) || (matchMode != "exact" && strings.HasPrefix(jsLower(canonicalCommand), jsLower(prefix)+" "))
	case "wildcard":
		return matchMode != "exact" && MatchWildcard(canonical+ruleRest, canonicalCommand)
	}
	return false
}
func MatchingRules(command string, rules []Rule, matchMode, behavior string) []Rule {
	// Source uses Map(ruleContent → Rule): retain first insertion position and
	// last metadata for duplicate contents rather than sorting source names.
	ordered := []Rule{}
	positions := map[string]int{}
	for _, rule := range rules {
		if rule.RuleValue.ToolName != "PowerShell" || rule.RuleBehavior != behavior {
			continue
		}
		content := rule.RuleValue.RuleContent
		if position, exists := positions[content]; exists {
			ordered[position] = rule
		} else {
			positions[content] = len(ordered)
			ordered = append(ordered, rule)
		}
	}
	matched := []Rule{}
	for _, rule := range ordered {
		if MatchesRule(command, rule.RuleValue.RuleContent, matchMode, behavior) {
			matched = append(matched, rule)
		}
	}
	return matched
}
func ExactSuggestions(command string) []any {
	if strings.ContainsAny(command, "\n*") {
		return []any{}
	}
	return []any{map[string]any{"type": "addRules", "rules": []RuleValue{{ToolName: "PowerShell", RuleContent: command}}, "behavior": "allow", "destination": "localSettings"}}
}
func RequestMessage(reason any) string {
	if value, ok := reason.(map[string]string); ok && value["type"] == "other" {
		return value["reason"]
	}
	return "Claude requested permissions to use PowerShell, but you haven't granted it yet."
}
