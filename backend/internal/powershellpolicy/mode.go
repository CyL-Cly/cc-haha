package powershellpolicy

import (
	"encoding/json"
	"strings"
)

type PermissionResult struct {
	Behavior       string          `json:"behavior"`
	Message        string          `json:"message,omitempty"`
	BlockedPath    string          `json:"blockedPath,omitempty"`
	UpdatedInput   json.RawMessage `json:"updatedInput,omitempty"`
	DecisionReason any             `json:"decisionReason,omitempty"`
	Suggestions    []any           `json:"suggestions,omitempty"`
}

func passthrough(message string) PermissionResult {
	return PermissionResult{Behavior: "passthrough", Message: message}
}

var acceptEditsWrites = set("set-content", "add-content", "remove-item", "clear-content")

func IsSymlinkCreatingCommand(command Command) bool {
	if ResolveCanonical(command.Name) != "new-item" {
		return false
	}
	for i, raw := range command.Args {
		normalized := raw
		for _, prefix := range []string{"-", "–", "—", "―", "/"} {
			if strings.HasPrefix(raw, prefix) {
				normalized = "-" + strings.TrimPrefix(raw, prefix)
				break
			}
		}
		lower := jsLower(strings.ReplaceAll(normalized, "`", ""))
		param, value, colon := strings.Cut(lower, ":")
		if len(param) < 3 || !(strings.HasPrefix("-itemtype", param) || strings.HasPrefix("-type", param)) {
			continue
		}
		if !colon && i+1 < len(command.Args) {
			value = jsLower(command.Args[i+1])
		}
		value = stripQuotes(strings.ReplaceAll(value, "`", ""))
		if value == "symboliclink" || value == "junction" || value == "hardlink" {
			return true
		}
	}
	return false
}
func (c *Classifier) CheckMode(input json.RawMessage, parsed Parsed, mode string) PermissionResult {
	if mode == "bypassPermissions" || mode == "dontAsk" {
		return passthrough("Mode is handled in main permission flow")
	}
	if mode != "acceptEdits" {
		return passthrough("No mode-specific validation required")
	}
	if !parsed.Valid {
		return passthrough("Cannot validate mode for unparsed command")
	}
	flags := DeriveSecurityFlags(parsed)
	if flags.HasSubExpressions || flags.HasScriptBlocks || flags.HasMemberInvocations || flags.HasSplatting || flags.HasAssignments || flags.HasStopParsing || flags.HasExpandableStrings {
		return passthrough("Command contains subexpressions, script blocks, or member invocations that require approval")
	}
	if len(parsed.Statements) == 0 {
		return passthrough("No commands found to validate for acceptEdits mode")
	}
	var original struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &original)
	count := 0
	for _, s := range parsed.Statements {
		count += len(s.Commands)
	}
	if count > 1 {
		cd, link, write := false, false, false
		for _, s := range parsed.Statements {
			for _, command := range s.Commands {
				if command.ElementType != "CommandAst" {
					continue
				}
				cd = cd || c.CwdChanging(command.Name)
				link = link || IsSymlinkCreatingCommand(command)
				write = write || acceptEditsWrites[ResolveCanonical(command.Name)]
			}
		}
		if cd && write {
			return passthrough("Compound command contains a directory-changing command (Set-Location/Push-Location/Pop-Location) with a write operation — cannot auto-allow because path validation uses stale cwd")
		}
		if link {
			return passthrough("Compound command creates a filesystem link (New-Item -ItemType SymbolicLink/Junction/HardLink) — cannot auto-allow because path validation cannot follow just-created links")
		}
	}
	for _, s := range parsed.Statements {
		for _, command := range s.Commands {
			if command.ElementType != "CommandAst" {
				return passthrough("Pipeline contains expression source (" + command.ElementType + ") that cannot be statically validated")
			}
			if command.NameType == "application" {
				return passthrough("Command '" + command.Name + "' resolved from a path-like name and requires approval")
			}
			for i := 1; i < len(command.ElementTypes); i++ {
				kind := command.ElementTypes[i]
				if kind != "StringConstant" && kind != "Parameter" {
					return passthrough("Command argument has unvalidatable type (" + kind + ") — variable paths cannot be statically resolved")
				}
				if kind == "Parameter" && i-1 < len(command.Args) {
					arg := command.Args[i-1]
					if colon := strings.Index(arg, ":"); colon > 0 && strings.ContainsAny(arg[colon+1:], "$(@{[") {
						return passthrough("Colon-bound parameter contains an expression that cannot be statically validated")
					}
				}
			}
			if c.SafeOutput(command.Name) || c.AllowlistedPipelineTail(command, original.Command) {
				continue
			}
			if !acceptEditsWrites[ResolveCanonical(command.Name)] {
				return passthrough("No mode-specific handling for '" + command.Name + "' in acceptEdits mode")
			}
			if ArgLeaksValue(command) {
				return passthrough("Arguments in '" + command.Name + "' cannot be statically validated in acceptEdits mode")
			}
		}
		for _, command := range s.NestedCommands {
			if command.ElementType != "CommandAst" {
				return passthrough("Nested expression element (" + command.ElementType + ") cannot be statically validated")
			}
			if command.NameType == "application" {
				return passthrough("Nested command '" + command.Name + "' resolved from a path-like name and requires approval")
			}
			if c.SafeOutput(command.Name) || c.AllowlistedPipelineTail(command, original.Command) {
				continue
			}
			if !acceptEditsWrites[ResolveCanonical(command.Name)] {
				return passthrough("No mode-specific handling for '" + command.Name + "' in acceptEdits mode")
			}
			if ArgLeaksValue(command) {
				return passthrough("Arguments in nested '" + command.Name + "' cannot be statically validated in acceptEdits mode")
			}
		}
	}
	return PermissionResult{Behavior: "allow", UpdatedInput: append(json.RawMessage{}, input...), DecisionReason: map[string]string{"type": "mode", "mode": "acceptEdits"}}
}
