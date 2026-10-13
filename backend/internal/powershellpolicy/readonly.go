package powershellpolicy

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

// Inert original CMDLET_ALLOWLIST data; algorithms and callbacks below remain
// native Go. Actual Source corpus compares every entry including callback IDs.
//
//go:embed cmdlet_allowlist.json
var cmdletAllowlistJSON []byte

type CmdletConfig struct {
	SafeFlags     []string `json:"safeFlags,omitempty"`
	AllowAllFlags bool     `json:"allowAllFlags,omitempty"`
	Callback      string   `json:"callback,omitempty"`
	Regex         string   `json:"regex,omitempty"`
}
type Classifier struct {
	Platform, UserType string
	Allowlist          map[string]CmdletConfig
	external           *sessionrewind.RecordedCommandClassifier
}

var commonParameters = set("-verbose", "-debug", "-erroraction", "-warningaction", "-informationaction", "-progressaction", "-errorvariable", "-warningvariable", "-informationvariable", "-outvariable", "-outbuffer", "-pipelinevariable")
var safeOutput = set("out-null")
var pipelineTail = set("format-table", "format-list", "format-wide", "format-custom", "measure-object", "select-object", "sort-object", "group-object", "where-object", "out-string", "out-host")

func NewClassifier(platform, userType string) (*Classifier, error) {
	c := &Classifier{Platform: platform, UserType: userType}
	if err := json.Unmarshal(cmdletAllowlistJSON, &c.Allowlist); err != nil {
		return nil, err
	}
	for name, config := range c.Allowlist {
		if config.Regex != "" {
			return nil, errors.New("unported Source cmdlet regex: " + name)
		}
		if config.Callback != "" && config.Callback != "argLeaksValue" && (name != "ipconfig" && name != "hostname" && name != "route") {
			return nil, errors.New("unported Source cmdlet callback: " + name)
		}
	}
	var err error
	c.external, err = sessionrewind.NewRecordedCommandClassifier(platform, userType)
	return c, err
}
func ResolveCanonical(name string) string {
	lower := jsLower(name)
	if !strings.ContainsAny(lower, `\/`) {
		for _, extension := range []string{".exe", ".cmd", ".bat", ".com"} {
			if strings.HasSuffix(lower, extension) {
				lower = strings.TrimSuffix(lower, extension)
				break
			}
		}
	}
	return Canonical(lower)
}
func (c *Classifier) CwdChanging(name string) bool {
	name = ResolveCanonical(name)
	return name == "set-location" || name == "push-location" || name == "pop-location" || name == "new-psdrive" || ((c.Platform == "windows" || c.Platform == "win32") && (name == "ndr" || name == "mount"))
}
func (c *Classifier) SafeOutput(name string) bool { return safeOutput[ResolveCanonical(name)] }
func (c *Classifier) AllowlistedPipelineTail(command Command, original string) bool {
	return pipelineTail[ResolveCanonical(command.Name)] && c.Allowlisted(command, original)
}
func ProvablySafeStatement(statement Statement) bool {
	if statement.StatementType != "PipelineAst" || len(statement.Commands) == 0 {
		return false
	}
	for _, command := range statement.Commands {
		if command.ElementType != "CommandAst" {
			return false
		}
	}
	return true
}
func ArgLeaksValue(command Command) bool {
	for i := 1; i < len(command.ElementTypes); i++ {
		kind := command.ElementTypes[i]
		arg := ""
		if i-1 < len(command.Args) {
			arg = command.Args[i-1]
		}
		if kind != "StringConstant" && kind != "Parameter" {
			if strings.ContainsAny(arg, "$(@{[") {
				return true
			}
			continue
		}
		if kind == "Parameter" {
			if i-1 < len(command.Children) && command.Children[i-1] != nil {
				for _, child := range command.Children[i-1] {
					if child.Type != "StringConstant" {
						return true
					}
				}
			} else if colon := strings.Index(arg, ":"); colon > 0 && strings.ContainsAny(arg[colon+1:], "$(@{[") {
				return true
			}
		}
	}
	return false
}
func (c *Classifier) Allowlisted(command Command, original string) bool {
	if command.NameType == "application" {
		tokens := strings.Fields(command.Text)
		if len(tokens) == 0 || !jsEqual(tokens[0], "where.exe") {
			return false
		}
	}
	config, ok := c.Allowlist[jsLower(command.Name)]
	if !ok {
		config, ok = c.Allowlist[ResolveCanonical(command.Name)]
	}
	if !ok {
		return false
	}
	switch config.Callback {
	case "argLeaksValue":
		if ArgLeaksValue(command) {
			return false
		}
	case "additionalCommandIsDangerousCallback":
		switch jsLower(command.Name) {
		case "ipconfig":
			for _, a := range command.Args {
				if !strings.HasPrefix(a, "/") && !strings.HasPrefix(a, "-") {
					return false
				}
			}
		case "hostname":
			for _, a := range command.Args {
				if !strings.HasPrefix(a, "-") {
					return false
				}
			}
		case "route":
			verb := ""
			for _, a := range command.Args {
				if !strings.HasPrefix(a, "-") {
					verb = a
					break
				}
			}
			if !jsEqual(verb, "print") {
				return false
			}
		}
	}
	if command.ElementTypes == nil || ArgLeaksValue(command) {
		return false
	}
	canonical := ResolveCanonical(command.Name)
	if canonical == "git" || canonical == "gh" || canonical == "docker" || canonical == "dotnet" {
		return c.externalSafe(canonical, command.Args)
	}
	isCmdlet := strings.Contains(canonical, "-")
	if config.AllowAllFlags {
		return true
	}
	for i, a := range command.Args {
		flag := strings.HasPrefix(a, "-") || ((c.Platform == "win32" || c.Platform == "windows") && strings.HasPrefix(a, "/"))
		if isCmdlet {
			flag = IsParameter(a, argType(command, i+1))
		}
		if !flag {
			continue
		}
		if len(config.SafeFlags) == 0 {
			return false
		}
		name := a
		if isCmdlet {
			for _, prefix := range []string{"-", "–", "—", "―"} {
				if strings.HasPrefix(a, prefix) {
					name = "-" + strings.TrimPrefix(a, prefix)
					break
				}
			}
		}
		if colon := strings.Index(name, ":"); colon > 0 {
			name = name[:colon]
		}
		lower := jsLower(name)
		if isCmdlet && commonParameters[lower] {
			continue
		}
		found := false
		for _, safe := range config.SafeFlags {
			found = found || jsEqual(safe, lower)
		}
		if !found {
			return false
		}
	}
	return true
}
func (c *Classifier) ReadOnly(command string, parsed Parsed) bool {
	if jsTrim(command) == "" || !parsed.Valid {
		return false
	}
	flags := DeriveSecurityFlags(parsed)
	if flags.HasScriptBlocks || flags.HasSubExpressions || flags.HasExpandableStrings || flags.HasSplatting || flags.HasMemberInvocations || flags.HasAssignments || flags.HasStopParsing {
		return false
	}
	if len(parsed.Statements) == 0 {
		return false
	}
	count := 0
	for _, s := range parsed.Statements {
		count += len(s.Commands)
	}
	if count > 1 {
		for _, s := range parsed.Statements {
			for _, command := range s.Commands {
				if c.CwdChanging(command.Name) {
					return false
				}
			}
		}
	}
	for _, s := range parsed.Statements {
		if len(s.Commands) == 0 {
			return false
		}
		for _, r := range s.Redirections {
			if !r.IsMerging && !IsNullRedirectionTarget(r.Target) {
				return false
			}
		}
		if !c.Allowlisted(s.Commands[0], command) {
			return false
		}
		for _, tail := range s.Commands[1:] {
			if tail.NameType == "application" {
				return false
			}
			if c.SafeOutput(tail.Name) && len(tail.Args) == 0 {
				continue
			}
			if !c.Allowlisted(tail, command) {
				return false
			}
		}
		if len(s.NestedCommands) > 0 {
			return false
		}
	}
	return true
}
func (c *Classifier) externalSafe(name string, args []string) bool {
	if name == "dotnet" {
		if len(args) == 0 {
			return false
		}
		for _, a := range args {
			if !set("--version", "--info", "--list-runtimes", "--list-sdks")[jsLower(a)] {
				return false
			}
		}
		return true
	}
	if name == "gh" && c.UserType != "ant" {
		return false
	}
	if len(args) == 0 {
		return true
	}
	for _, a := range args {
		if strings.Contains(a, "$") {
			return false
		}
	}
	if name == "docker" && (jsEqual(args[0], "ps") || jsEqual(args[0], "images")) {
		return true
	}
	index := 0
	if name == "git" {
		dangerous := set("-c", "-C", "--exec-path", "--config-env", "--git-dir", "--work-tree", "--attr-source")
		takesValue := set("-c", "-C", "--exec-path", "--config-env", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--shallow-file")
		for index < len(args) && args[index] != "" && strings.HasPrefix(args[index], "-") {
			a := args[index]
			if len(a) > 2 && (strings.HasPrefix(a, "-C") || strings.HasPrefix(a, "-c") && a[2] != '-') {
				return false
			}
			flag, _, inline := strings.Cut(a, "=")
			if dangerous[flag] {
				return false
			}
			if !inline && takesValue[flag] {
				index += 2
			} else {
				index++
			}
		}
		if index >= len(args) {
			return true
		}
	}
	first := jsLower(args[index])
	second := ""
	if index+1 < len(args) {
		second = jsLower(args[index+1])
	}
	for _, length := range []int{2, 1} {
		if index+length > len(args) {
			continue
		}
		key := name + " " + first
		if length == 2 {
			key += " " + second
		}
		flags := args[index+length:]
		if name == "git" && first == "ls-remote" {
			for _, a := range flags {
				if !strings.HasPrefix(a, "-") && strings.ContainsAny(a, "@:$") {
					return false
				}
			}
		}
		allowed, found, err := c.external.ExternalReadOnlyArgs(key, flags)
		if found {
			return err == nil && allowed
		}
	}
	return false
}
