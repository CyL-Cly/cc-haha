package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

// PermissionDebugData is a read-only snapshot from the actual permission host.
// Terminal display never rebuilds policy or turns debug keys into approval.
type PermissionDebugData struct {
	Result, Context  json.RawMessage
	DebugHint        bool
	SandboxAutoAllow bool
}

func (s *State) PermissionDebugVisible(request PermissionRequest) bool {
	return request.Debug != nil && request.ID != "" && s.permissionDebugID == request.ID && (request.ToolName == "Bash" || request.ToolName == "PowerShell")
}

func (s *State) TogglePermissionDebug() bool {
	if len(s.Permissions) == 0 {
		return false
	}
	request := s.Permissions[0]
	if request.Debug == nil || (request.ToolName != "Bash" && request.ToolName != "PowerShell") {
		return false
	}
	if s.PermissionDebugVisible(request) {
		s.permissionDebugID = ""
	} else {
		s.permissionDebugID = request.ID
	}
	return true
}

func permissionDebugSuggestions(value json.RawMessage) ([]protocol.JSONObject, []string, string) {
	var updates []protocol.JSONObject
	_ = json.Unmarshal(value, &updates)
	rules, directories, mode := []protocol.JSONObject{}, []string{}, ""
	for _, update := range updates {
		switch update.String("type") {
		case "addRules":
			var added []protocol.JSONObject
			_ = json.Unmarshal(update.Get("rules"), &added)
			rules = append(rules, added...)
		case "addDirectories":
			var added []string
			_ = json.Unmarshal(update.Get("directories"), &added)
			directories = append(directories, added...)
		case "setMode":
			mode = update.String("mode")
		}
	}
	return rules, directories, mode
}

func permissionDebugReason(value protocol.JSONObject) string {
	switch value.String("type") {
	case "classifier":
		return value.String("classifier") + " classifier: " + value.String("reason")
	case "rule":
		rule, _ := value.Object("rule")
		content, _ := rule.Object("ruleValue")
		return sdkpermissions.PermissionRuleDisplay(content) + " rule from " + sdkpermissions.PermissionRuleSourceDisplay(rule.String("source"))
	case "mode":
		return sdkpermissions.PermissionModeDisplay(value.String("mode")) + " mode"
	case "sandboxOverride":
		return "Requires permission to bypass sandbox"
	case "workingDir", "safetyCheck", "other", "asyncAgent":
		return value.String("reason")
	case "permissionPromptTool":
		return value.String("permissionPromptToolName") + " permission prompt tool"
	case "hook":
		result := value.String("hookName") + " hook"
		if reason := value.String("reason"); reason != "" {
			result += ": " + reason
		}
		return result
	}
	return ""
}

// PermissionDebugLines renders a read-only view of the actual decision and
// context. The transient debug view never changes the permission result.
func PermissionDebugLines(request PermissionRequest, width int) []string {
	if request.Debug == nil {
		return nil
	}
	result := presentationObject(request.Debug.Result)
	context := presentationObject(request.Debug.Context)
	rows := []string{}
	column := func(label string, contents []string) {
		prefix := strings.Repeat(" ", max(0, 10-exportWidth(label)-1)) + label + " "
		if len(contents) == 0 {
			contents = []string{""}
		}
		first := true
		for _, content := range contents {
			for _, line := range exportWrapWords(content, max(1, width-10)) {
				lead := strings.Repeat(" ", 10)
				if first {
					lead, first = prefix, false
				}
				rows = append(rows, strings.TrimRight(lead+line, " "))
			}
		}
	}
	column("Behavior", []string{result.String("behavior")})
	if result.String("behavior") != "allow" {
		column("Message", []string{result.String("message")})
	}
	reason, _ := result.Object("decisionReason")
	if result.Get("decisionReason") == nil {
		column("Reason", []string{"undefined"})
	} else if reason.String("type") != "subcommandResults" {
		column("Reason", []string{permissionDebugReason(reason)})
	} else {
		var entries [][]json.RawMessage
		_ = json.Unmarshal(reason.Get("entries"), &entries)
		contents := []string{}
		for _, entry := range entries {
			if len(entry) != 2 {
				continue
			}
			command, _ := presentationString(entry[0])
			decision := presentationObject(entry[1])
			icon := "✘"
			if decision.String("behavior") == "allow" {
				icon = "✔"
			}
			contents = append(contents, icon+" "+command)
			child, _ := decision.Object("decisionReason")
			if decision.Get("decisionReason") != nil && child.String("type") != "subcommandResults" {
				contents = append(contents, "  ⎿  "+permissionDebugReason(child))
			}
			if decision.String("behavior") == "ask" {
				rules, _, _ := permissionDebugSuggestions(decision.Get("suggestions"))
				values := []string{}
				for _, rule := range rules {
					values = append(values, sdkpermissions.PermissionRuleDisplay(rule))
				}
				if len(values) > 0 {
					contents = append(contents, "  ⎿  Suggested rules: "+strings.Join(values, ", "))
				}
			}
		}
		column("Reason", contents)
	}
	rules, directories, mode := permissionDebugSuggestions(result.Get("suggestions"))
	var suggestions []json.RawMessage
	_ = json.Unmarshal(result.Get("suggestions"), &suggestions)
	if len(suggestions) == 0 {
		column("Suggestions", []string{"None"})
	} else if len(rules) == 0 && len(directories) == 0 && mode == "" {
		column("Suggestion", []string{"None"})
	} else {
		column("Suggestions", []string{" "})
		if len(rules) > 0 {
			values := []string{}
			for _, rule := range rules {
				values = append(values, "● "+sdkpermissions.PermissionRuleDisplay(rule))
			}
			column("Rules", values)
		}
		if len(directories) > 0 {
			values, natural := []string{}, 0
			for _, directory := range directories {
				value := "● " + directory
				values = append(values, value)
				natural = max(natural, exportWidth(value))
			}
			// The actual label includes both separator spaces and can exceed
			// the shared minWidth=10. Its row shrinks the label to that minimum
			// before wrapping the independent directory column.
			left, rightWidth := float64(13), float64(natural)
			if total := left + rightWidth; total > float64(width) {
				left *= float64(max(0, width)) / total
				rightWidth *= float64(max(0, width)) / total
				if left < 10 {
					left, rightWidth = 10, float64(max(0, width-10))
				}
			}
			rightStart := int(math.Round(left))
			// The label's lone text child keeps its measured integral basis
			// inside the fractional flex box rather than stretching its glyphs.
			labels := exportWrapWords(" Directories ", max(1, int(math.Floor(left))))
			_, height := exportMeasuredText(" Directories ", left)
			right := []string{}
			rightHeight := 0
			for _, value := range values {
				_, measured := exportMeasuredText(value, rightWidth)
				rightHeight += measured
				visible := exportWrapWords(value, max(1, int(math.Ceil(left+rightWidth)-math.Floor(left))))
				right = append(right, visible...)
				for len(visible) < measured {
					right = append(right, "")
					measured--
				}
			}
			for index := 0; index < max(height, rightHeight); index++ {
				a, b := "", ""
				if index < len(labels) {
					a = labels[index]
				}
				if index < len(right) {
					b = right[index]
				}
				if b != "" {
					a = exportTruncate(a, rightStart, false)
				}
				rows = append(rows, strings.TrimRight(exportPad(a, rightStart)+b, " "))
			}
		}
		if mode != "" {
			column("Mode", []string{sdkpermissions.PermissionModeDisplay(mode)})
		}
	}
	unreachable := []sdkpermissions.UnreachablePermissionRule{}
	for _, rule := range sdkpermissions.DetectUnreachablePermissionRules(context, request.Debug.SandboxAutoAllow) {
		keep := rule.Rule.String("toolName") == request.ToolName || request.ToolName == ""
		if len(rules) > 0 {
			keep = false
			for _, suggested := range rules {
				keep = keep || suggested.String("toolName") == rule.Rule.String("toolName") && string(suggested.Get("ruleContent")) == string(rule.Rule.Get("ruleContent"))
			}
		}
		if keep {
			unreachable = append(unreachable, rule)
		}
	}
	if len(unreachable) > 0 {
		rows = append(rows, "")
		rows = append(rows, exportWrapWords(fmt.Sprintf("⚠ Unreachable Rules (%d)", len(unreachable)), width)...)
		for _, rule := range unreachable {
			contents := []string{sdkpermissions.PermissionRuleDisplay(rule.Rule), "  " + rule.Reason, "  Fix: " + rule.Fix}
			// Source measures this column's flex basis before subtracting its
			// marginLeft, then paints at the narrower stretched width. Reuse
			// the same measurement/wrapping boundary as the native export.
			// Fractional text positions floor; later siblings can overwrite an
			// earlier wrapped row in the original narrow-terminal layout.
			height, total := 0, 0
			basis := make([]int, len(contents))
			for index, content := range contents {
				_, natural := exportMeasuredText(content, float64(max(1, width)))
				height += natural
				_, basis[index] = exportMeasuredText(content, float64(max(1, width-2)))
				total += basis[index]
			}
			grid := make([]string, height)
			scale, position := math.Min(1, float64(height)/float64(max(1, total))), float64(0)
			for index, content := range contents {
				top := int(math.Floor(position))
				for offset, line := range exportWrapWords(content, max(1, width-2)) {
					if y := top + offset; y < len(grid) {
						grid[y] = "  " + line
					}
				}
				position += float64(basis[index]) * scale
			}
			rows = append(rows, grid...)
		}
	}
	return rows
}
