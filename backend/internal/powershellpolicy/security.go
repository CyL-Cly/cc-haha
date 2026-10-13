package powershellpolicy

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

type SecurityResult struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

func ask(message string) SecurityResult { return SecurityResult{Behavior: "ask", Message: message} }
func pass() SecurityResult              { return SecurityResult{Behavior: "passthrough"} }
func set(values ...string) map[string]bool {
	result := map[string]bool{}
	for _, v := range values {
		result[v] = true
	}
	return result
}

var fileExecution = set("invoke-command", "start-job", "start-threadjob", "register-scheduledjob")
var dangerousScriptBlocks = set("invoke-command", "invoke-expression", "start-job", "start-threadjob", "register-scheduledjob", "register-engineevent", "register-objectevent", "register-wmievent", "new-pssession", "enter-pssession")
var moduleLoading = set("import-module", "ipmo", "install-module", "save-module", "update-module", "install-script", "save-script")
var safeScriptBlocks = set("where-object", "sort-object", "select-object", "group-object", "format-table", "format-list", "format-wide", "format-custom")
var scheduledTasks = set("register-scheduledtask", "new-scheduledtask", "new-scheduledtaskaction", "set-scheduledtask")
var envWrites = set("set-item", "si", "new-item", "ni", "remove-item", "ri", "del", "rm", "rd", "rmdir", "erase", "clear-item", "cli", "set-content", "add-content", "ac")
var runtimeWrites = set("set-alias", "sal", "new-alias", "nal", "set-variable", "sv", "new-variable", "nv")
var wmiSpawn = set("invoke-wmimethod", "iwmi", "invoke-cimmethod")
var downloaders = set("invoke-webrequest", "iwr", "invoke-restmethod", "irm", "new-object", "start-bitstransfer")
var psExecutables = set("pwsh", "pwsh.exe", "powershell", "powershell.exe")
var verbColon = regexp.MustCompile(`^[-–—―/]V[A-Z]*:`)
var elevatedColon = regexp.MustCompile("^[-–—―/]V[A-Z]*:['\"` ]*RUNAS['\"` ]*$")
var genericType = regexp.MustCompile(`\[.*\]$`)

func IsClmAllowedType(name string) bool {
	name = jsLower(name)
	name = strings.TrimSuffix(name, "[]")
	name = genericType.ReplaceAllString(name, "")
	return clmAllowedTypes[jsTrim(name)]
}
func isPowerShellExecutable(name string) bool {
	name = jsLower(name)
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	return psExecutables[name]
}
func psParam(command Command, full, minPrefix string) bool {
	if HasArgAbbreviation(command, full, minPrefix) {
		return true
	}
	copy := command
	copy.Args = append([]string{}, command.Args...)
	for i, arg := range copy.Args {
		for _, prefix := range []string{"/", "–", "—", "―"} {
			if strings.HasPrefix(arg, prefix) {
				copy.Args[i] = "-" + strings.TrimPrefix(arg, prefix)
				break
			}
		}
	}
	return HasArgAbbreviation(copy, full, minPrefix)
}
func argType(command Command, index int) string {
	if index >= 0 && index < len(command.ElementTypes) {
		return command.ElementTypes[index]
	}
	return ""
}
func stripQuotes(value string) string {
	if strings.HasPrefix(value, "'") || strings.HasPrefix(value, `"`) {
		value = value[1:]
	}
	if strings.HasSuffix(value, "'") || strings.HasSuffix(value, `"`) {
		value = value[:len(value)-1]
	}
	return value
}
func hasArg(command Command, values ...string) bool {
	for _, a := range command.Args {
		for _, v := range values {
			if jsEqual(a, v) {
				return true
			}
		}
	}
	return false
}
func newObjectType(command Command) (string, bool) {
	for i, a := range command.Args {
		lower := jsLower(a)
		if strings.HasPrefix(lower, "-t") {
			if colon := strings.Index(lower, ":"); colon >= 0 {
				if strings.HasPrefix("-typename", lower[:colon]) {
					return a[colon+1:], true
				}
			}
			if strings.HasPrefix("-typename", lower) && i+1 < len(command.Args) {
				return command.Args[i+1], true
			}
		}
	}
	for i := 0; i < len(command.Args); i++ {
		a := command.Args[i]
		lower := jsLower(a)
		if strings.HasPrefix(a, "-") {
			if strings.HasPrefix(lower, "-t") && strings.HasPrefix("-typename", lower) {
				i++
				continue
			}
			if strings.Contains(lower, ":") || lower == "-strict" {
				continue
			}
			if lower == "-argumentlist" || lower == "-comobject" || lower == "-property" {
				i++
			}
			continue
		}
		return a, true
	}
	return "", false
}

// Security preserves the Source's ordered validators and explanatory result.
// It requests the existing permission prompt; it never adds a separate deny
// policy or executes the command being analyzed.
func Security(parsed Parsed) SecurityResult {
	if !parsed.Valid {
		return ask("Could not parse command for security analysis")
	}
	commands := AllCommands(parsed)
	if HasCommandNamed(parsed, "Invoke-Expression") {
		return ask("Command uses Invoke-Expression which can execute arbitrary code")
	}
	for _, c := range commands {
		if c.ElementType == "CommandAst" && len(c.ElementTypes) > 0 && c.ElementTypes[0] != "StringConstant" {
			return ask("Command name is a dynamic expression which cannot be statically validated")
		}
	}
	for _, c := range commands {
		if isPowerShellExecutable(c.Name) && psParam(c, "-encodedcommand", "-e") {
			return ask("Command uses encoded parameters which obscure intent")
		}
	}
	for _, c := range commands {
		if isPowerShellExecutable(c.Name) {
			return ask("Command spawns a nested PowerShell process which cannot be validated")
		}
	}
	for _, s := range parsed.Statements {
		download, iex := false, false
		for _, c := range s.Commands {
			lower := jsLower(c.Name)
			download = download || downloaders[lower]
			iex = iex || lower == "invoke-expression" || lower == "iex"
		}
		if len(s.Commands) >= 2 && download && iex {
			return ask("Command downloads and executes remote code")
		}
	}
	download, iex := false, false
	for _, c := range commands {
		lower := jsLower(c.Name)
		download = download || downloaders[lower]
		iex = iex || lower == "invoke-expression" || lower == "iex"
	}
	if download && iex {
		return ask("Command downloads and executes remote code")
	}
	for _, c := range commands {
		lower := jsLower(c.Name)
		if lower == "start-bitstransfer" {
			return ask("Command downloads files via BITS transfer")
		}
		if (lower == "certutil" || lower == "certutil.exe") && hasArg(c, "-urlcache", "/urlcache") {
			return ask("Command uses certutil to download from a URL")
		}
		if (lower == "bitsadmin" || lower == "bitsadmin.exe") && hasArg(c, "/transfer") {
			return ask("Command downloads files via BITS transfer")
		}
	}
	if HasCommandNamed(parsed, "Add-Type") {
		return ask("Command compiles and loads .NET code")
	}
	for _, c := range commands {
		if !jsEqual(c.Name, "new-object") {
			continue
		}
		if psParam(c, "-comobject", "-com") {
			return ask("Command instantiates a COM object which may have execution capabilities")
		}
		if name, ok := newObjectType(c); ok && !IsClmAllowedType(name) {
			return ask(fmt.Sprintf("New-Object instantiates .NET type '%s' outside the ConstrainedLanguage allowlist", name))
		}
	}
	for _, c := range commands {
		if !fileExecution[Canonical(c.Name)] {
			continue
		}
		if psParam(c, "-filepath", "-f") || psParam(c, "-literalpath", "-l") {
			return ask(c.Name + " -FilePath executes an arbitrary script file")
		}
		for i, a := range c.Args {
			if argType(c, i+1) == "StringConstant" && a != "" && !strings.HasPrefix(a, "-") {
				return ask(c.Name + " with positional string argument binds to -FilePath and executes a script file")
			}
		}
	}
	for _, c := range commands {
		if jsEqual(c.Name, "invoke-item") || jsEqual(c.Name, "ii") {
			return ask("Invoke-Item opens files with the default handler (ShellExecute). On executable files this runs arbitrary code.")
		}
	}
	for _, c := range commands {
		lower := jsLower(c.Name)
		if scheduledTasks[lower] {
			return ask(c.Name + " creates or modifies a scheduled task (persistence primitive)")
		}
		if (lower == "schtasks" || lower == "schtasks.exe") && hasArg(c, "/create", "/change", "-create", "-change") {
			return ask("schtasks with create/change modifies scheduled tasks (persistence primitive)")
		}
	}
	for _, c := range commands {
		if Canonical(c.Name) != "foreach-object" {
			continue
		}
		if psParam(c, "-membername", "-m") {
			return ask("ForEach-Object -MemberName invokes methods by string name which cannot be validated")
		}
		for i, a := range c.Args {
			if argType(c, i+1) == "StringConstant" && a != "" && !strings.HasPrefix(a, "-") {
				return ask("ForEach-Object with positional string argument binds to -MemberName and invokes methods by name")
			}
		}
	}
	for _, c := range commands {
		lower := jsLower(c.Name)
		if lower != "start-process" && lower != "saps" && lower != "start" {
			continue
		}
		if psParam(c, "-Verb", "-v") && hasArg(c, "runas") {
			return ask("Command requests elevated privileges")
		}
		for i, a := range c.Args {
			clean := strings.ReplaceAll(a, "`", "")
			if verbColon.MatchString(ecmaRegexFold(clean)) && i < len(c.Children) {
				for _, child := range c.Children[i] {
					value := strings.Map(func(r rune) rune {
						if r == '\'' || r == '"' || r == '`' || unicode.IsSpace(r) {
							return -1
						}
						return r
					}, child.Text)
					if jsEqual(value, "runas") {
						return ask("Command requests elevated privileges")
					}
				}
			}
			if elevatedColon.MatchString(ecmaRegexFold(clean)) {
				return ask("Command requests elevated privileges")
			}
		}
		for _, a := range c.Args {
			if isPowerShellExecutable(stripQuotes(a)) {
				return ask("Start-Process launches a nested PowerShell process which cannot be validated")
			}
		}
	}
	flags := DeriveSecurityFlags(parsed)
	if flags.HasScriptBlocks {
		for _, c := range commands {
			if dangerousScriptBlocks[jsLower(c.Name)] {
				return ask("Command contains script block with dangerous cmdlet that may execute arbitrary code")
			}
		}
		for _, c := range commands {
			if !safeScriptBlocks[Canonical(c.Name)] {
				return ask("Command contains script block that may execute arbitrary code")
			}
		}
	}
	if flags.HasSubExpressions {
		return ask("Command contains subexpressions $()")
	}
	if flags.HasExpandableStrings {
		return ask("Command contains expandable strings with embedded expressions")
	}
	if flags.HasSplatting {
		return ask("Command uses splatting (@variable)")
	}
	if flags.HasStopParsing {
		return ask("Command uses stop-parsing token (--%)")
	}
	if flags.HasMemberInvocations {
		return ask("Command invokes .NET methods")
	}
	for _, name := range parsed.TypeLiterals {
		if !IsClmAllowedType(name) {
			return ask("Command uses .NET type [" + name + "] outside the ConstrainedLanguage allowlist")
		}
	}
	hasEnv := false
	for _, v := range parsed.Variables {
		hasEnv = hasEnv || strings.HasPrefix(jsLower(v.Path), "env:")
	}
	if hasEnv {
		for _, c := range commands {
			if envWrites[jsLower(c.Name)] {
				return ask("Command modifies environment variables")
			}
		}
		if flags.HasAssignments {
			return ask("Command modifies environment variables")
		}
	}
	for _, c := range commands {
		if moduleLoading[jsLower(c.Name)] {
			return ask("Command loads, installs, or downloads a PowerShell module or script, which can execute arbitrary code")
		}
	}
	for _, c := range commands {
		name := jsLower(c.Name)
		if i := strings.LastIndex(name, `\`); i >= 0 {
			name = name[i+1:]
		}
		if runtimeWrites[name] {
			return ask("Command creates or modifies an alias or variable that can affect future command resolution")
		}
	}
	for _, c := range commands {
		if wmiSpawn[jsLower(c.Name)] {
			return ask(c.Name + " can spawn arbitrary processes via WMI/CIM (Win32_Process Create)")
		}
	}
	return pass()
}
