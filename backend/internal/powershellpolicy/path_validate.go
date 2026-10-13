package powershellpolicy

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
)

type PathCheck struct {
	Allowed        bool   `json:"allowed"`
	ResolvedPath   string `json:"resolvedPath"`
	DecisionReason any    `json:"decisionReason,omitempty"`
}

// PathContext is the existing filesystem permission boundary supplied by the
// owning native tool service. Resolve/Check are real filesystem operations;
// classification has no implicit user HOME, settings or environment access.
type PathContext struct {
	Platform, CWD, Home, Mode string
	WorkingDirectories        []string
	Resolve                   func(string) (resolved string, canonical bool, err error)
	Check                     func(resolved, operation string, canonical bool) (PathCheck, error)
	Deny                      func(resolved, operation string) (*Rule, error)
	Directory                 func(string) string
}

func SafeResolvePath(value string) (string, bool, error) {
	if nativepaths.IsUNC(value) {
		return value, false, nil
	}
	info, err := os.Lstat(value)
	if err != nil || info.Mode()&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice|os.ModeCharDevice) != 0 {
		return value, false, nil
	}
	if canonical, err := filepath.EvalSymlinks(value); err == nil {
		return canonical, true, nil
	}
	return value, false, nil
}

var slashRuns = regexp.MustCompile(`[\\/]+`)
var windowsDriveRoot = regexp.MustCompile(`^[A-Za-z]:/?$`)
var windowsDriveChild = regexp.MustCompile(`^[A-Za-z]:/[^/]+$`)
var providerWindows = regexp.MustCompile(`^[A-Z0-9]{2,}:`)
var providerPOSIX = regexp.MustCompile(`^[A-Z0-9]+:`)

func DangerousRemovalPath(value, home string) bool {
	value = slashRuns.ReplaceAllString(value, "/")
	if value == "*" || strings.HasSuffix(value, "/*") {
		return true
	}
	if value != "/" {
		value = strings.TrimSuffix(value, "/")
	}
	return value == "/" || windowsDriveRoot.MatchString(value) || value == slashRuns.ReplaceAllString(home, "/") || path.Dir(value) == "/" || windowsDriveChild.MatchString(value)
}
func expandTilde(value, home string) string {
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		return home + value[1:]
	}
	return value
}
func DangerousRemovalRawPath(value, home string) bool {
	return DangerousRemovalPath(strings.ReplaceAll(expandTilde(stripQuotes(value), home), `\`, "/"), home)
}
func DangerousRemovalDeny(value string) PermissionResult {
	return PermissionResult{Behavior: "deny", Message: "Remove-Item on system path '" + value + "' is blocked. This path is protected from removal.", DecisionReason: map[string]string{"type": "other", "reason": "Removal targets a protected system path"}}
}
func (c PathContext) absolute(value string) string {
	if c.Platform == "windows" || c.Platform == "win32" {
		return nativepaths.ResolveWindows(value, c.CWD)
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(c.CWD, value)
}
func (c PathContext) directory(value string) string {
	if c.Directory != nil {
		return c.Directory(value)
	}
	if c.Platform == "windows" || c.Platform == "win32" {
		return nativepaths.DirWindows(value)
	}
	return filepath.Dir(value)
}
func reason(kind, message string) map[string]string {
	return map[string]string{"type": kind, "reason": message}
}
func (c PathContext) denyGuess(value, operation string) (*PathCheck, error) {
	if value == "" || strings.ContainsRune(value, 0) {
		return nil, nil
	}
	if c.Resolve == nil || c.Deny == nil {
		return nil, errors.New("PowerShell paths require owned canonical resolution and deny rules")
	}
	resolved, _, err := c.Resolve(c.absolute(expandTilde(value, c.Home)))
	if err != nil {
		return nil, err
	}
	rule, err := c.Deny(resolved, operation)
	if err != nil {
		return nil, err
	}
	if rule != nil {
		return &PathCheck{ResolvedPath: resolved, DecisionReason: map[string]any{"type": "rule", "rule": *rule}}, nil
	}
	return nil, nil
}
func (c PathContext) ValidatePath(value, operation string) (PathCheck, error) {
	normalized := strings.ReplaceAll(expandTilde(stripQuotes(value), c.Home), `\`, "/")
	other := func(message string) PathCheck {
		return PathCheck{ResolvedPath: normalized, DecisionReason: reason("other", message)}
	}
	if strings.Contains(normalized, "`") {
		guess, e := c.denyGuess(strings.ReplaceAll(normalized, "`", ""), operation)
		if e != nil {
			return PathCheck{}, e
		}
		if guess != nil {
			return *guess, nil
		}
		return other("Backtick escape characters in paths cannot be statically validated and require manual approval"), nil
	}
	if at := strings.Index(normalized, "::"); at >= 0 {
		guess, e := c.denyGuess(normalized[at+2:], operation)
		if e != nil {
			return PathCheck{}, e
		}
		if guess != nil {
			return *guess, nil
		}
		return other("Module-qualified provider paths (::) cannot be statically validated and require manual approval"), nil
	}
	if strings.HasPrefix(normalized, "//") || strings.Contains(jsLower(normalized), "davwwwroot") || strings.Contains(jsLower(normalized), "@ssl@") {
		return other("UNC paths are blocked because they can trigger network requests and credential leakage"), nil
	}
	if strings.ContainsAny(normalized, "$%") {
		return other("Variable expansion syntax in paths requires manual approval"), nil
	}
	provider := providerPOSIX
	if c.Platform == "windows" || c.Platform == "win32" {
		provider = providerWindows
	}
	if provider.MatchString(ecmaRegexFold(normalized)) {
		return other("Path '" + normalized + "' uses a non-filesystem provider and requires manual approval"), nil
	}
	if c.Resolve == nil || c.Check == nil || c.Deny == nil {
		return PathCheck{}, errors.New("PowerShell paths require owned filesystem permission callbacks")
	}
	if strings.ContainsAny(normalized, "*?[]") {
		if operation != "read" {
			return other("Glob patterns are not allowed in write operations. Please specify an exact file path."), nil
		}
		traversal := false
		for _, part := range strings.Split(normalized, "/") {
			traversal = traversal || part == ".."
		}
		if !traversal {
			at := strings.IndexAny(normalized, "*?[]")
			if at < 0 {
				at = len(normalized)
			}
			base := path.Dir(normalized[:at])
			if before := strings.LastIndex(normalized[:at], "/"); before >= 0 {
				base = normalized[:before]
				if base == "" {
					base = "/"
				}
			}
			resolved, _, err := c.Resolve(c.absolute(base))
			if err != nil {
				return PathCheck{}, err
			}
			rule, e := c.Deny(resolved, operation)
			if e != nil {
				return PathCheck{}, e
			}
			if rule != nil {
				return PathCheck{ResolvedPath: resolved, DecisionReason: map[string]any{"type": "rule", "rule": *rule}}, nil
			}
			return PathCheck{ResolvedPath: resolved, DecisionReason: reason("other", "Glob patterns in paths cannot be statically validated — symlinks inside the glob expansion are not examined. Requires manual approval.")}, nil
		}
	}
	resolved, canonical, err := c.Resolve(c.absolute(normalized))
	if err != nil {
		return PathCheck{}, err
	}
	checked, err := c.Check(resolved, operation, canonical)
	checked.ResolvedPath = resolved
	return checked, err
}
func reasonFields(value any) (kind, message string) {
	switch r := value.(type) {
	case map[string]string:
		return r["type"], r["reason"]
	case map[string]any:
		kind, _ = r["type"].(string)
		message, _ = r["reason"].(string)
	}
	return
}
func (c PathContext) directoryList() string {
	list := []string{}
	for i, dir := range c.WorkingDirectories {
		if i == 5 {
			break
		}
		list = append(list, "'"+dir+"'")
	}
	text := strings.Join(list, ", ")
	if len(c.WorkingDirectories) > 5 {
		text += fmt.Sprintf(", and %d more", len(c.WorkingDirectories)-5)
	}
	return text
}
func (c PathContext) pathDecision(checked PathCheck, operation, command string, redirection bool) PermissionResult {
	kind, message := reasonFields(checked.DecisionReason)
	if kind != "other" && kind != "safetyCheck" {
		if redirection {
			message = "Output redirection to '" + checked.ResolvedPath + "' was blocked. For security, Claude Code may only write to files in the allowed working directories for this session: " + c.directoryList() + "."
		} else {
			message = command + " targeting '" + checked.ResolvedPath + "' was blocked. For security, Claude Code may only access files in the allowed working directories for this session: " + c.directoryList() + "."
		}
	}
	if kind == "rule" {
		return PermissionResult{Behavior: "deny", Message: message, DecisionReason: checked.DecisionReason}
	}
	suggestions := []any{}
	dir := c.directory(checked.ResolvedPath)
	if redirection || operation != "read" {
		suggestions = append(suggestions, map[string]any{"type": "addDirectories", "directories": []string{dir}, "destination": "session"})
	} else {
		posix := dir
		if c.Platform == "windows" || c.Platform == "win32" {
			posix = nativepaths.WindowsToPOSIX(dir)
		}
		if posix != "/" {
			content := posix + "/**"
			if strings.HasPrefix(posix, "/") {
				content = "/" + content
			}
			suggestions = append(suggestions, map[string]any{"type": "addRules", "rules": []RuleValue{{ToolName: "Read", RuleContent: content}}, "behavior": "allow", "destination": "session"})
		}
	}
	if !redirection && operation != "read" {
		suggestions = append(suggestions, map[string]string{"type": "setMode", "mode": "acceptEdits", "destination": "session"})
	}
	return PermissionResult{Behavior: "ask", Message: message, BlockedPath: checked.ResolvedPath, DecisionReason: checked.DecisionReason, Suggestions: suggestions}
}
func (c PathContext) CheckConstraints(parsed Parsed, configs map[string]PathConfig, compoundCD bool) (PermissionResult, error) {
	if !parsed.Valid {
		return passthrough("Cannot validate paths for unparsed command"), nil
	}
	var firstAsk *PermissionResult
	keep := func(result PermissionResult) {
		if firstAsk == nil && result.Behavior == "ask" {
			copy := result
			firstAsk = &copy
		}
	}
	for _, statement := range parsed.Statements {
		var statementAsk *PermissionResult
		if compoundCD {
			result := PermissionResult{Behavior: "ask", Message: "Compound command changes working directory (Set-Location/Push-Location/Pop-Location/New-PSDrive) — relative paths cannot be validated against the original cwd and require manual approval", DecisionReason: reason("other", "Compound command contains cd with path operation — manual approval required to prevent path resolution bypass")}
			statementAsk = &result
		}
		remember := func(result PermissionResult) {
			if statementAsk == nil && result.Behavior == "ask" {
				copy := result
				statementAsk = &copy
			}
		}
		expression, source := false, ""
		checkCommand := func(command Command, nested bool) (*PermissionResult, error) {
			extracted := ExtractPaths(command, configs)
			canonical := ResolveCanonical(command.Name)
			if !nested && expression {
				guess, err := c.denyGuess(stripQuotes(source), extracted.OperationType)
				if err != nil {
					return nil, err
				}
				if guess != nil {
					result := PermissionResult{Behavior: "deny", Message: canonical + " targeting '" + guess.ResolvedPath + "' was blocked by a deny rule", DecisionReason: guess.DecisionReason}
					return &result, nil
				}
				remember(PermissionResult{Behavior: "ask", Message: canonical + " receives its path from a pipeline expression source that cannot be statically validated and requires manual approval"})
			}
			if extracted.HasUnvalidatablePathArg {
				remember(PermissionResult{Behavior: "ask", Message: canonical + " uses a parameter or complex path expression (array literal, subexpression, unknown parameter, etc.) that cannot be statically validated and requires manual approval"})
			}
			_, configured := configs[canonical]
			if extracted.OperationType != "read" && !extracted.OptionalWrite && len(extracted.Paths) == 0 && configured {
				remember(PermissionResult{Behavior: "ask", Message: canonical + " is a write operation but no target path could be determined; requires manual approval"})
				return nil, nil
			}
			for _, value := range extracted.Paths {
				removal := canonical == "remove-item"
				if removal && DangerousRemovalRawPath(value, c.Home) {
					result := DangerousRemovalDeny(value)
					return &result, nil
				}
				checked, err := c.ValidatePath(value, extracted.OperationType)
				if err != nil {
					return nil, err
				}
				if removal && DangerousRemovalPath(checked.ResolvedPath, c.Home) {
					result := DangerousRemovalDeny(checked.ResolvedPath)
					return &result, nil
				}
				if !checked.Allowed {
					decision := c.pathDecision(checked, extracted.OperationType, canonical, false)
					if decision.Behavior == "deny" {
						return &decision, nil
					}
					remember(decision)
				}
			}
			if nested && expression {
				remember(PermissionResult{Behavior: "ask", Message: canonical + " appears inside a control-flow or chain statement where piped expression sources cannot be statically validated and requires manual approval"})
			}
			return nil, nil
		}
		for _, command := range statement.Commands {
			if command.ElementType != "CommandAst" {
				expression = true
				source = command.Text
				continue
			}
			decision, err := checkCommand(command, false)
			if err != nil {
				return PermissionResult{}, err
			}
			if decision != nil {
				return *decision, nil
			}
		}
		for _, command := range statement.NestedCommands {
			decision, err := checkCommand(command, true)
			if err != nil {
				return PermissionResult{}, err
			}
			if decision != nil {
				return *decision, nil
			}
		}
		redirections := []Redirection{}
		for _, command := range statement.NestedCommands {
			redirections = append(redirections, command.Redirections...)
		}
		redirections = append(redirections, statement.Redirections...)
		for _, redirect := range redirections {
			if redirect.IsMerging || redirect.Target == "" || IsNullRedirectionTarget(redirect.Target) {
				continue
			}
			checked, err := c.ValidatePath(redirect.Target, "create")
			if err != nil {
				return PermissionResult{}, err
			}
			if !checked.Allowed {
				decision := c.pathDecision(checked, "create", "", true)
				if decision.Behavior == "deny" {
					return decision, nil
				}
				remember(decision)
			}
		}
		if statementAsk != nil {
			keep(*statementAsk)
		}
	}
	if firstAsk != nil {
		return *firstAsk, nil
	}
	return passthrough("All path constraints validated successfully"), nil
}
