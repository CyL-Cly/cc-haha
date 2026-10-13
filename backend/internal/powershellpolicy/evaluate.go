package powershellpolicy

import (
	"encoding/json"
	"regexp"
	"strings"
)

type EvaluationContext struct {
	Mode           string
	Rules          []Rule
	Paths          PathContext
	BareRepository bool
}
type subCommand struct {
	text       string
	element    Command
	statement  *Statement
	safeOutput bool
}

var nonFSProvider = regexp.MustCompile(`(?i)^(?:[\w.]+\\)?(env|hklm|hkcu|function|alias|variable|cert|wsman|registry)::?`)
var psAssignment = regexp.MustCompile(`^\$[\w:]+\s*(?:[+\-*/%]|\?\?)?\s*=\s*`)
var continuation = regexp.MustCompile("`[\r\n]+\\s*")
var fragmentSeparator = regexp.MustCompile(`[;|\n\r{}()&]+`)
var gitWrites = set("new-item", "set-content", "add-content", "out-file", "copy-item", "move-item", "rename-item", "expand-archive", "invoke-webrequest", "invoke-restmethod", "tee-object", "export-csv", "export-clixml")
var archiveExtractors = set("tar", "tar.exe", "bsdtar", "bsdtar.exe", "unzip", "unzip.exe", "7z", "7z.exe", "7za", "7za.exe", "gzip", "gzip.exe", "gunzip", "gunzip.exe", "expand-archive")

func ruleResult(command string, input json.RawMessage, rule Rule) PermissionResult {
	result := PermissionResult{Behavior: rule.RuleBehavior, DecisionReason: map[string]any{"type": "rule", "rule": rule}}
	if rule.RuleBehavior == "deny" {
		result.Message = "Permission to use PowerShell with command " + jsTrim(command) + " has been denied."
	} else if rule.RuleBehavior == "ask" {
		result.Message = RequestMessage(nil)
	} else {
		result.UpdatedInput = append(json.RawMessage{}, input...)
	}
	return result
}
func rulesDecision(command string, input json.RawMessage, rules []Rule, matchMode string) PermissionResult {
	for _, behavior := range []string{"deny", "ask", "allow"} {
		if matched := MatchingRules(command, rules, matchMode, behavior); len(matched) > 0 {
			return ruleResult(command, input, matched[0])
		}
	}
	r := reason("other", "This command requires approval")
	return PermissionResult{Behavior: "passthrough", Message: RequestMessage(r), DecisionReason: r, Suggestions: ExactSuggestions(jsTrim(command))}
}
func commandsForPermission(parsed *Parsed, c *Classifier, original string) []subCommand {
	result := []subCommand{}
	for i := range parsed.Statements {
		s := &parsed.Statements[i]
		for _, command := range s.Commands {
			if command.ElementType != "CommandAst" {
				continue
			}
			result = append(result, subCommand{command.Text, command, s, command.NameType != "application" && c.SafeOutput(command.Name) && len(command.Args) == 0})
		}
		for _, command := range s.NestedCommands {
			result = append(result, subCommand{command.Text, command, s, command.NameType != "application" && c.SafeOutput(command.Name) && len(command.Args) == 0})
		}
	}
	if len(result) == 0 {
		names := AllCommandNames(*parsed)
		name := ""
		if len(names) > 0 {
			name = names[0]
		}
		result = append(result, subCommand{text: original, element: Command{Name: name, NameType: "unknown", ElementType: "CommandAst", Args: []string{}, Text: original}})
	}
	return result
}
func vulnerableUNC(value, platform string) bool {
	if platform != "windows" && platform != "win32" {
		return false
	}
	// Source network path predicate is raw syntax; no filesystem/network probe.
	for _, pattern := range []string{`\\\\[^\s\\/]+(?:@(?:\d+|ssl))?(?:[\\/]|$|\s)`, `(?:^|[^:])//[^\s\\/]+(?:@(?:\d+|ssl))?(?:[\\/]|$|\s)`, `/\\{2,}[^\s\\/]`, `\\{2,}/[^\s\\/]`, `@SSL@\d+`, `@\d+@SSL`, `DavWWWRoot`} {
		if regexp.MustCompile("(?i)" + pattern).MatchString(value) {
			return true
		}
	}
	return false
}
func (c *Classifier) Evaluate(input json.RawMessage, parsed Parsed, context EvaluationContext) (PermissionResult, error) {
	var value struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &value); err != nil {
		return PermissionResult{}, err
	}
	command := jsTrim(value.Command)
	if command == "" {
		return PermissionResult{Behavior: "allow", UpdatedInput: input, DecisionReason: reason("other", "Empty command is safe")}, nil
	}
	exact := rulesDecision(command, input, context.Rules, "exact")
	if exact.Behavior == "deny" {
		return exact, nil
	}
	if deny := MatchingRules(command, context.Rules, "prefix", "deny"); len(deny) > 0 {
		return ruleResult(command, input, deny[0]), nil
	}
	var preAsk *PermissionResult
	if askRules := MatchingRules(command, context.Rules, "prefix", "ask"); len(askRules) > 0 {
		result := ruleResult(command, input, askRules[0])
		preAsk = &result
	} else if vulnerableUNC(command, context.Paths.Platform) {
		result := PermissionResult{Behavior: "ask", Message: "Command contains a UNC path that could trigger network requests"}
		preAsk = &result
	}
	if exact.Behavior == "allow" && !parsed.Valid && preAsk == nil {
		head, _ := commandHead(command)
		if classifyCommandName(head) != "application" {
			return exact, nil
		}
	}
	if !parsed.Valid {
		normalized := strings.ReplaceAll(continuation.ReplaceAllString(command, ""), "`", "")
		for _, fragment := range fragmentSeparator.Split(normalized, -1) {
			fragment = jsTrim(fragment)
			if fragment == "" {
				continue
			}
			if fragment == command && !regexp.MustCompile(`^\$[\w:]`).MatchString(fragment) && !regexp.MustCompile(`^[&.]\s`).MatchString(fragment) {
				continue
			}
			for {
				next := psAssignment.ReplaceAllString(fragment, "")
				if next == fragment {
					break
				}
				fragment = next
			}
			fragment = regexp.MustCompile(`^[&.]\s+`).ReplaceAllString(fragment, "")
			head, rest := commandHead(fragment)
			head = stripQuotes(head)
			fragment = head + rest
			if ResolveCanonical(head) == "remove-item" {
				for _, arg := range strings.Fields(fragment)[1:] {
					if IsParameter(arg, "") {
						continue
					}
					if DangerousRemovalRawPath(arg, context.Paths.Home) {
						return DangerousRemovalDeny(arg), nil
					}
				}
			}
			if deny := MatchingRules(fragment, context.Rules, "prefix", "deny"); len(deny) > 0 {
				return ruleResult(command, input, deny[0]), nil
			}
		}
		if preAsk != nil {
			return *preAsk, nil
		}
		message := "unknown error"
		if len(parsed.Errors) > 0 {
			message = parsed.Errors[0].Message
		}
		r := reason("other", "Command contains malformed syntax that cannot be parsed: "+message)
		return PermissionResult{Behavior: "ask", Message: RequestMessage(r), DecisionReason: r}, nil
	}
	subs := commandsForPermission(&parsed, c, command)
	decisions := []PermissionResult{}
	if preAsk != nil {
		decisions = append(decisions, *preAsk)
	}
	safety := Security(parsed)
	if safety.Behavior != "passthrough" {
		r := reason("other", safety.Message)
		decisions = append(decisions, PermissionResult{Behavior: "ask", Message: RequestMessage(r), DecisionReason: r, Suggestions: ExactSuggestions(command)})
	}
	for _, flag := range []struct {
		enabled bool
		message string
	}{{parsed.HasUsingStatements, "Command contains a `using` statement that may load external code (module or assembly)"}, {parsed.HasScriptRequirements, "Command contains a `#Requires` directive that may trigger module loading"}} {
		if flag.enabled {
			r := reason("other", flag.message)
			decisions = append(decisions, PermissionResult{Behavior: "ask", Message: RequestMessage(r), DecisionReason: r, Suggestions: ExactSuggestions(command)})
		}
	}
	providerFound := false
	for _, s := range parsed.Statements {
		commands := []Command{}
		for _, command := range s.Commands {
			if command.ElementType == "CommandAst" {
				commands = append(commands, command)
			}
		}
		commands = append(commands, s.NestedCommands...)
		for _, element := range commands {
			for _, arg := range element.Args {
				provider := arg
				if IsParameter(provider, "") {
					if at := strings.Index(provider, ":"); at > 0 {
						provider = provider[at+1:]
					}
				}
				provider = strings.ReplaceAll(provider, "`", "")
				if nonFSProvider.MatchString(provider) {
					decisions = append(decisions, PermissionResult{Behavior: "ask", Message: "Command argument '" + arg + "' uses a non-filesystem provider path and requires approval"})
					providerFound = true
					break
				}
				if vulnerableUNC(provider, context.Paths.Platform) {
					decisions = append(decisions, PermissionResult{Behavior: "ask", Message: "Command argument '" + arg + "' contains a UNC path that could trigger network requests"})
					providerFound = true
					break
				}
			}
			if providerFound {
				break
			}
		}
		if providerFound {
			break
		}
	}
	for _, sub := range subs {
		deny := MatchingRules(sub.text, context.Rules, "prefix", "deny")
		askRules := MatchingRules(sub.text, context.Rules, "prefix", "ask")
		if len(deny) == 0 && sub.element.Name != "" {
			canonical := strings.Join(append([]string{sub.element.Name}, sub.element.Args...), " ")
			deny = MatchingRules(canonical, context.Rules, "prefix", "deny")
			if len(askRules) == 0 {
				askRules = MatchingRules(canonical, context.Rules, "prefix", "ask")
			}
		}
		if len(deny) > 0 {
			decisions = append(decisions, ruleResult(command, input, deny[0]))
		} else if len(askRules) > 0 {
			decisions = append(decisions, ruleResult(command, input, askRules[0]))
		}
	}
	cd, link, git := false, false, false
	for _, sub := range subs {
		cd = cd || len(subs) > 1 && c.CwdChanging(sub.element.Name)
		link = link || len(subs) > 1 && IsSymlinkCreatingCommand(sub.element)
		git = git || ResolveCanonical(sub.element.Name) == "git"
	}
	if cd && git {
		decisions = append(decisions, PermissionResult{Behavior: "ask", Message: "Compound commands with cd/Set-Location and git require approval to prevent bare repository attacks"})
	}
	if git && context.BareRepository {
		decisions = append(decisions, PermissionResult{Behavior: "ask", Message: "Git command in a directory with bare-repository indicators (HEAD, objects/, refs/ in cwd without .git/HEAD). Git may execute hooks from cwd."})
	}
	internalWrite, dotWrite, archive := false, false, false
	for _, sub := range subs {
		for _, redirect := range sub.element.Redirections {
			internalWrite = internalWrite || context.Paths.gitPath(redirect.Target, false)
			dotWrite = dotWrite || context.Paths.gitPath(redirect.Target, true)
		}
		if gitWrites[ResolveCanonical(sub.element.Name)] {
			for _, arg := range sub.element.Args {
				for _, path := range strings.Split(arg, ",") {
					internalWrite = internalWrite || context.Paths.gitPath(path, false)
					dotWrite = dotWrite || context.Paths.gitPath(path, true)
				}
			}
			if sub.statement != nil {
				for _, element := range sub.statement.Commands {
					if element.ElementType != "CommandAst" {
						internalWrite = internalWrite || context.Paths.gitPath(element.Text, false)
					}
				}
			}
		}
		archive = archive || archiveExtractors[jsLower(sub.element.Name)]
	}
	for _, redirect := range FileRedirections(parsed) {
		internalWrite = internalWrite || context.Paths.gitPath(redirect.Target, false)
		dotWrite = dotWrite || context.Paths.gitPath(redirect.Target, true)
	}
	if git && internalWrite {
		decisions = append(decisions, PermissionResult{Behavior: "ask", Message: "Command writes to a git-internal path (HEAD, objects/, refs/, hooks/, .git/) and runs git. This could plant a malicious hook that git then executes."})
	}
	if git && archive {
		decisions = append(decisions, PermissionResult{Behavior: "ask", Message: "Compound command extracts an archive and runs git. Archive contents may plant bare-repository indicators (HEAD, hooks/, refs/) that git then treats as the repository root."})
	}
	if dotWrite {
		decisions = append(decisions, PermissionResult{Behavior: "ask", Message: "Command writes to .git/ — hooks or config planted there execute on the next git operation."})
	}
	configs, err := PathConfigs()
	if err != nil {
		return PermissionResult{}, err
	}
	paths, err := context.Paths.CheckConstraints(parsed, configs, cd)
	if err != nil {
		return PermissionResult{}, err
	}
	if paths.Behavior != "passthrough" {
		decisions = append(decisions, paths)
	}
	if exact.Behavior == "allow" && len(subs) > 0 {
		allowed := true
		for _, sub := range subs {
			allowed = allowed && sub.element.NameType != "application" && !ArgLeaksValue(sub.element)
		}
		if allowed {
			decisions = append(decisions, exact)
		}
	}
	if c.ReadOnly(command, parsed) {
		decisions = append(decisions, PermissionResult{Behavior: "allow", UpdatedInput: input, DecisionReason: reason("other", "Command is read-only and safe to execute")})
	}
	if len(FileRedirections(parsed)) > 0 {
		decisions = append(decisions, PermissionResult{Behavior: "ask", Message: "Command contains file redirections that could write to arbitrary paths", Suggestions: ExactSuggestions(command)})
	}
	mode := c.CheckMode(input, parsed, context.Mode)
	if mode.Behavior != "passthrough" {
		decisions = append(decisions, mode)
	}
	for _, behavior := range []string{"deny", "ask", "allow"} {
		for _, decision := range decisions {
			if decision.Behavior == behavior {
				return decision, nil
			}
		}
	}
	pending := []string{}
	seen := map[*Statement]bool{}
	for _, sub := range subs {
		if sub.safeOutput {
			continue
		}
		if sub.element.NameType != "application" && ResolveCanonical(sub.element.Name) == "set-location" && len(sub.element.Args) > 0 {
			target := ""
			for _, a := range sub.element.Args {
				if !IsParameter(a, "") {
					target = a
					break
				}
			}
			if target != "" && context.Paths.absolute(target) == context.Paths.CWD {
				continue
			}
		}
		push := func() {
			if sub.statement != nil {
				seen[sub.statement] = true
			}
			pending = append(pending, sub.text)
		}
		data, _ := json.Marshal(map[string]string{"command": sub.text})
		result := rulesDecision(sub.text, data, context.Rules, "exact")
		if result.Behavior != "deny" && result.Behavior != "ask" {
			prefix := rulesDecision(sub.text, data, context.Rules, "prefix")
			if prefix.Behavior == "deny" || prefix.Behavior == "ask" || result.Behavior != "allow" && prefix.Behavior != "passthrough" {
				result = prefix
			}
		}
		if result.Behavior == "deny" {
			result.Message = "Permission to use PowerShell with command " + command + " has been denied."
			return result, nil
		}
		if result.Behavior == "ask" {
			push()
			continue
		}
		if result.Behavior == "allow" {
			if sub.element.NameType != "application" && !link && !ArgLeaksValue(sub.element) {
				continue
			}
			push()
			continue
		}
		if sub.statement != nil && !cd && !link && ProvablySafeStatement(*sub.statement) && c.Allowlisted(sub.element, sub.text) {
			continue
		}
		if sub.statement != nil && !cd && !link {
			single := Parsed{Valid: true, Errors: []ParseError{}, Variables: parsed.Variables, HasStopParsing: parsed.HasStopParsing, OriginalCommand: sub.text, Statements: []Statement{*sub.statement}}
			if c.CheckMode(data, single, context.Mode).Behavior == "allow" {
				continue
			}
		}
		push()
	}
	for i := range parsed.Statements {
		statement := &parsed.Statements[i]
		if !ProvablySafeStatement(*statement) && !seen[statement] {
			pending = append(pending, statement.Text)
		}
	}
	if len(pending) == 0 {
		if DeriveSecurityFlags(parsed).HasScriptBlocks {
			return PermissionResult{Behavior: "ask", Message: RequestMessage(nil), DecisionReason: reason("other", "Pipeline consists of output-formatting cmdlets with script blocks — block content cannot be verified")}, nil
		}
		return PermissionResult{Behavior: "allow", UpdatedInput: input, DecisionReason: reason("other", "All pipeline commands are individually allowed")}, nil
	}
	suggestions := []any{}
	for _, sub := range pending {
		suggestions = append(suggestions, ExactSuggestions(sub)...)
	}
	r := reason("other", "This command requires approval")
	return PermissionResult{Behavior: "passthrough", Message: RequestMessage(r), DecisionReason: r, Suggestions: suggestions}, nil
}
