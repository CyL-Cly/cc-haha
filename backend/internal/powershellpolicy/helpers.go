package powershellpolicy

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"strings"
)

func jsTrim(value string) string { return protocol.TrimJavaScriptString(value) }

func Canonical(name string) string {
	lower := jsLower(name)
	if alias := CommonAliases[lower]; alias != "" {
		return jsLower(alias)
	}
	return lower
}
func AllCommands(parsed Parsed) []Command {
	commands := []Command{}
	for _, s := range parsed.Statements {
		commands = append(commands, s.Commands...)
		commands = append(commands, s.NestedCommands...)
	}
	return commands
}
func AllCommandNames(parsed Parsed) []string {
	names := []string{}
	for _, c := range AllCommands(parsed) {
		names = append(names, jsLower(c.Name))
	}
	return names
}
func HasCommandNamed(parsed Parsed, name string) bool {
	lower := jsLower(name)
	alias := jsLower(CommonAliases[lower])
	for _, c := range AllCommands(parsed) {
		command := jsLower(c.Name)
		resolved := jsLower(CommonAliases[command])
		if command == lower || resolved == lower || alias != "" && command == alias || resolved != "" && alias != "" && resolved == alias {
			return true
		}
	}
	return false
}
func AllRedirections(parsed Parsed) []Redirection {
	redirections := []Redirection{}
	for _, s := range parsed.Statements {
		redirections = append(redirections, s.Redirections...)
		for _, c := range s.NestedCommands {
			redirections = append(redirections, c.Redirections...)
		}
	}
	return redirections
}
func IsNullRedirectionTarget(target string) bool {
	t := jsLower(jsTrim(target))
	return t == "$null" || t == "${null}"
}
func FileRedirections(parsed Parsed) []Redirection {
	result := []Redirection{}
	for _, r := range AllRedirections(parsed) {
		if !r.IsMerging && !IsNullRedirectionTarget(r.Target) {
			result = append(result, r)
		}
	}
	return result
}
func HasDirectoryChange(parsed Parsed) bool {
	for _, c := range AllCommands(parsed) {
		switch jsLower(c.Name) {
		case "set-location", "push-location", "pop-location", "cd", "sl", "chdir", "pushd", "popd":
			return true
		}
	}
	return false
}
func IsSingleCommand(parsed Parsed) bool {
	return len(parsed.Statements) == 1 && len(parsed.Statements[0].Commands) == 1 && len(parsed.Statements[0].NestedCommands) == 0
}
func HasArgAbbreviation(command Command, full, minPrefix string) bool {
	full = jsLower(full)
	minPrefix = jsLower(minPrefix)
	for _, a := range command.Args {
		if i := strings.Index(a[minimum(1, len(a)):], ":"); i >= 0 {
			a = a[:i+1]
		}
		lower := jsLower(strings.ReplaceAll(a, "`", ""))
		if strings.HasPrefix(lower, minPrefix) && strings.HasPrefix(full, lower) && len(lower) <= len(full) {
			return true
		}
	}
	return false
}
func minimum(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func IsParameter(arg string, elementType string) bool {
	if elementType != "" {
		return elementType == "Parameter"
	}
	return strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "–") || strings.HasPrefix(arg, "—") || strings.HasPrefix(arg, "―")
}
func DeriveSecurityFlags(parsed Parsed) SecurityFlags {
	flags := SecurityFlags{HasStopParsing: parsed.HasStopParsing}
	check := func(command Command) {
		for _, kind := range command.ElementTypes {
			switch kind {
			case "ScriptBlock":
				flags.HasScriptBlocks = true
			case "SubExpression":
				flags.HasSubExpressions = true
			case "ExpandableString":
				flags.HasExpandableStrings = true
			case "MemberInvocation":
				flags.HasMemberInvocations = true
			}
		}
	}
	for _, s := range parsed.Statements {
		if s.StatementType == "AssignmentStatementAst" {
			flags.HasAssignments = true
		}
		for _, c := range s.Commands {
			check(c)
		}
		for _, c := range s.NestedCommands {
			check(c)
		}
		if p := s.SecurityPatterns; p != nil {
			flags.HasMemberInvocations = flags.HasMemberInvocations || p.HasMemberInvocations
			flags.HasSubExpressions = flags.HasSubExpressions || p.HasSubExpressions
			flags.HasExpandableStrings = flags.HasExpandableStrings || p.HasExpandableStrings
			flags.HasScriptBlocks = flags.HasScriptBlocks || p.HasScriptBlocks
		}
	}
	for _, v := range parsed.Variables {
		if v.IsSplatted {
			flags.HasSplatting = true
			break
		}
	}
	return flags
}

// Source compares toLowerCase strings; Unicode SimpleFold (e.g. long s) is different.
func jsLower(value string) string { return cases.Lower(language.Und).String(value) }
func jsEqual(a, b string) bool    { return jsLower(a) == jsLower(b) }
