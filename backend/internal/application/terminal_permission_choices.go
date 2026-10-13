package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/powershellpolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalSuggestionLabel(suggestions []protocol.JSONObject) string {
	labels := []string{}
	for _, suggestion := range suggestions {
		switch suggestion.String("type") {
		case "addRules":
			var rows []protocol.JSONObject
			_ = json.Unmarshal(suggestion.Get("rules"), &rows)
			for _, row := range rows {
				label := row.String("toolName")
				if content := row.String("ruleContent"); content != "" {
					label += "(" + content + ")"
				}
				labels = append(labels, label)
			}
		case "addDirectories":
			var rows []string
			_ = json.Unmarshal(suggestion.Get("directories"), &rows)
			labels = append(labels, rows...)
		case "setMode":
			labels = append(labels, "permission mode "+suggestion.String("mode"))
		}
	}
	if len(labels) == 0 {
		return ""
	}
	return "Yes, allow " + strings.Join(labels, ", ")
}
func terminalShellSuggestionLabel(suggestions []protocol.JSONObject, shell, originalCWD, platform string) string {
	commands, readPaths, directories := []string{}, []string{}, []string{}
	seen := map[string]bool{}
	for _, suggestion := range suggestions {
		if suggestion.String("type") == "addDirectories" {
			var paths []string
			_ = json.Unmarshal(suggestion.Get("directories"), &paths)
			directories = append(directories, paths...)
		}
		if suggestion.String("type") != "addRules" {
			continue
		}
		var rules []protocol.JSONObject
		_ = json.Unmarshal(suggestion.Get("rules"), &rules)
		for _, rule := range rules {
			content := rule.String("ruleContent")
			if content == "" {
				continue
			}
			if rule.String("toolName") == "Read" {
				readPaths = append(readPaths, strings.Replace(content, "/**", "", 1))
			}
			if rule.String("toolName") == shell {
				command := content
				parsed := powershellpolicy.ParseRule(content)
				if parsed.Type == "prefix" {
					command = parsed.Prefix
				}
				if !seen[command] {
					commands = append(commands, command)
					seen[command] = true
				}
			}
		}
	}
	separator := "/"
	base := filepath.Base
	if platform == "windows" {
		separator = `\`
		base = func(value string) string {
			value = strings.TrimRight(value, `\/`)
			if at := strings.LastIndexAny(value, `\/`); at >= 0 {
				return value[at+1:]
			}
			if len(value) == 2 && value[1] == ':' {
				return ""
			}
			return value
		}
	}
	formatPaths := func(paths []string) string {
		names := []string{}
		for _, path := range paths {
			name := base(path)
			if name == "" || name == "." {
				name = path
			}
			names = append(names, name+separator)
		}
		if len(names) == 1 {
			return names[0]
		}
		if len(names) == 2 {
			return names[0] + " and " + names[1]
		}
		if len(names) > 2 {
			return names[0] + ", " + names[1] + " and " + fmt.Sprint(len(names)-2) + " more"
		}
		return ""
	}
	commandList := func() string {
		joined := strings.Join(commands, ", ")
		if len(utf16.Encode([]rune(joined))) > 50 {
			return "similar"
		}
		if len(commands) == 1 {
			return commands[0]
		}
		if len(commands) == 2 {
			return commands[0] + " and " + commands[1]
		}
		if len(commands) > 2 {
			return strings.Join(commands[:len(commands)-1], ", ") + ", and " + commands[len(commands)-1]
		}
		return ""
	}
	paths := append(append([]string{}, directories...), readPaths...)
	if len(readPaths) > 0 && len(directories) == 0 && len(commands) == 0 {
		return "Yes, allow reading from " + formatPaths(readPaths) + " from this project"
	}
	if len(directories) > 0 && len(readPaths) == 0 && len(commands) == 0 {
		return "Yes, and always allow access to " + formatPaths(directories) + " from this project"
	}
	if len(commands) > 0 && len(paths) == 0 {
		return "Yes, and don't ask again for " + commandList() + " commands in " + originalCWD
	}
	if len(paths) > 0 && len(commands) == 0 {
		return "Yes, and always allow access to " + formatPaths(paths) + " from this project"
	}
	if len(paths) > 0 && len(commands) > 0 {
		if len(paths) == 1 && len(commands) == 1 {
			return "Yes, and allow access to " + formatPaths(paths) + " and " + commandList() + " commands"
		}
		return "Yes, and allow " + formatPaths(paths) + " access and " + commandList() + " commands"
	}
	return ""
}
func powerShellSuggestionsEditable(suggestions []protocol.JSONObject) bool {
	for _, suggestion := range suggestions {
		if suggestion.String("type") == "addDirectories" {
			return false
		}
		if suggestion.String("type") == "addRules" {
			var rules []protocol.JSONObject
			if json.Unmarshal(suggestion.Get("rules"), &rules) != nil {
				return false
			}
			for _, rule := range rules {
				if rule.String("toolName") != "PowerShell" {
					return false
				}
			}
		}
	}
	return true
}
func (s *Session) prepareTerminalPermissionChoices(scope *cooperative.Scope, request *tui.PermissionRequest) error {
	if !s.terminalInteractive || request.ToolName == "AskUserQuestion" || request.ToolName == "ExitPlanMode" {
		return nil
	}
	choices := []tui.PermissionChoice{{Key: "y", Label: "Yes", Decision: tui.PermissionDecision{Allowed: true}}}
	var suggestions []protocol.JSONObject
	if json.Unmarshal(obj(request.Payload).Get("permissionSuggestions"), &suggestions) == nil && len(suggestions) > 0 {
		input := obj(request.Input)
		show, prefixes, err := s.process.TerminalPermissionRuleOptions(scope, request.ToolName, input.String("command"))
		if err != nil {
			return err
		}
		if show {
			initial := (*string)(nil)
			if request.ToolName == "PowerShell" && powerShellSuggestionsEditable(suggestions) {
				initial = builtintools.PowerShellApprovalInitialPrefix(input.String("command"), prefixes)
			}
			if initial != nil {
				choices = append(choices, tui.PermissionChoice{Label: "Yes, and don’t ask again for", Editable: true, InitialValue: *initial, Decision: tui.PermissionDecision{Allowed: true}})
			} else {
				label := terminalSuggestionLabel(suggestions)
				if request.ToolName == "PowerShell" || request.ToolName == "Bash" {
					label = terminalShellSuggestionLabel(suggestions, request.ToolName, s.native.CWD, s.terminalPlatform)
				}
				if label != "" {
					choices = append(choices, tui.PermissionChoice{Label: label, Decision: tui.PermissionDecision{Allowed: true, PermissionUpdates: obj(request.Payload).Get("permissionSuggestions")}})
				}
			}
		}
	}
	choices = append(choices, tui.PermissionChoice{Key: "n", Label: "No", Decision: tui.PermissionDecision{Allowed: false}})
	request.Choices = choices
	return nil
}
func terminalEditedPermissionUpdates(request tui.PermissionRequest, decision *tui.PermissionDecision) error {
	if decision.EditedPrefix == nil {
		return nil
	}
	if !decision.Allowed || request.ToolName != "PowerShell" {
		return errors.New("edited prefix does not belong to an approved PowerShell request")
	}
	editable := false
	for _, choice := range request.Choices {
		editable = editable || choice.Editable
	}
	if !editable {
		return errors.New("permission request has no editable PowerShell prefix")
	}
	decision.PermissionUpdates = raw(builtintools.PowerShellEditedPermissionUpdates(*decision.EditedPrefix))
	return nil
}
