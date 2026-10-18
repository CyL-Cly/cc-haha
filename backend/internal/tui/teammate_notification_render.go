package tui

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

func terminalTeammateCard(content []string, width int, theme, role string, paddingY bool) []string {
	width = max(4, width)
	inner := width - 4
	border := func(text string) string { return terminalTaskText(text, theme, role, false, false) }
	rows := []string{"", border("╭" + strings.Repeat("─", width-2) + "╮")}
	if paddingY {
		content = append(append([]string{""}, content...), "")
	}
	for _, line := range content {
		rows = append(rows, border("│")+" "+line+strings.Repeat(" ", max(0, inner-exportWidth(line)))+" "+border("│"))
	}
	return append(rows, border("╰"+strings.Repeat("─", width-2)+"╯"), "")
}

func terminalTeammateDashed(content []string, width int, theme string) []string {
	border := terminalTaskText(strings.Repeat("╌", max(1, width)), theme, "subtle", false, false)
	rows := []string{border}
	for _, line := range content {
		rows = append(rows, " "+line)
	}
	return append(rows, border)
}

func terminalTeammateNotificationLines(row protocol.JSONObject, width int, theme string, origin TeammateMessageOrigin) ([]string, bool) {
	body := presentationObject([]byte(row.String("text")))
	kind, parsed := teamspawn.ParseInteractiveProtocol(row.String("text"))
	inner := max(1, width-4)
	plain := func(text string) []string { return exportWrapWords(text, inner) }
	colored := func(text, role string, bold bool) []string {
		return terminalMarkdownWrap(terminalTaskText(text, theme, role, bold, false), inner)
	}
	if kind == "plan_approval_request" {
		content := colored("Plan Approval Request from "+parsed.String("from"), "planMode", true)
		content = append(content, "")
		plan := TerminalMarkdownRows(parsed.String("planContent"), max(1, inner-2), width, theme)
		content = append(content, terminalTeammateDashed(plan, inner, theme)...)
		content = append(content, "")
		content = append(content, colored("Plan file: "+parsed.String("planFilePath"), "inactive", false)...)
		return terminalTeammateCard(content, width, theme, "planMode", false), true
	}
	if kind == "plan_approval_response" {
		approved := string(parsed.Get("approved")) == "true"
		if approved {
			content := colored("✓ Plan Approved by "+row.String("from"), "success", true)
			content = append(content, "")
			content = append(content, plain("You can now proceed with implementation. Your plan mode restrictions have been lifted.")...)
			return terminalTeammateCard(content, width, theme, "success", true), true
		}
		content := colored("✗ Plan Rejected by "+row.String("from"), "error", true)
		if feedback := parsed.String("feedback"); feedback != "" {
			content = append(content, "")
			content = append(content, terminalTeammateDashed(exportWrapWords("Feedback: "+feedback, max(1, inner-2)), inner, theme)...)
		}
		content = append(content, "")
		content = append(content, colored("Please revise your plan based on the feedback and call ExitPlanMode again.", "inactive", false)...)
		return terminalTeammateCard(content, width, theme, "error", true), true
	}
	if origin == TeammateMessageAttachment {
		if body.String("type") == "task_assignment" {
			by := body.String("assignedBy")
			if by == "" {
				by = row.String("from")
			}
			parts := []string{"⏺ ", "Task assigned: ", terminalTaskText("#"+body.String("taskId"), theme, "", true, false), " - " + body.String("subject"), terminalTaskText(" (from "+by+")", theme, "inactive", false, false)}
			rows := terminalTeammateHeaderLines(parts, max(1, width-2))
			for i := range rows {
				rows[i] = "  " + rows[i]
			}
			return rows, true
		}
		return nil, false
	}
	if kind == "shutdown_request" {
		content := colored("Shutdown request from "+parsed.String("from"), "warning", true)
		content = append(content, "")
		if reason := parsed.String("reason"); reason != "" {
			content = append(content, plain("Reason: "+reason)...)
		}
		return terminalTeammateCard(content, width, theme, "warning", true), true
	}
	if body.String("type") == "shutdown_rejected" && terminalTeammateStringFields(body, "requestId", "from", "reason", "timestamp") {
		content := colored("Shutdown rejected by "+body.String("from"), "subtle", true)
		content = append(content, "")
		content = append(content, terminalTeammateDashed(exportWrapWords("Reason: "+body.String("reason"), max(1, inner-2)), inner, theme)...)
		content = append(content, "")
		content = append(content, colored("Teammate is continuing to work. You may request shutdown again later.", "inactive", false)...)
		return terminalTeammateCard(content, width, theme, "subtle", true), true
	}
	if body.String("type") == "task_assignment" {
		content := colored("Task #"+body.String("taskId")+" assigned by "+body.String("assignedBy"), "cyan_FOR_SUBAGENTS_ONLY", true)
		content = append(content, "")
		content = append(content, colored(body.String("subject"), "", true)...)
		if description := body.String("description"); description != "" {
			content = append(content, "")
			content = append(content, colored(description, "inactive", false)...)
		}
		return terminalTeammateCard(content, width, theme, "cyan_FOR_SUBAGENTS_ONLY", true), true
	}
	if body.String("type") == "task_completed" {
		withoutSummary := row.Clone()
		withoutSummary.Delete("summary")
		header := terminalTeammatePlainMessageLines(withoutSummary, width, theme, false)
		text := " Completed task #" + body.String("taskId")
		if subject := body.String("taskSubject"); subject != "" {
			text += terminalTaskText(" ("+subject+")", theme, "inactive", false, false)
		}
		content := terminalFooterFlexRow([]string{terminalTaskText("✓", theme, "success", false, false), text}, max(1, width-5))
		for index, line := range content {
			prefix := "     "
			if index == 0 {
				prefix = terminalTaskText("  ⎿ \u00a0", theme, "inactive", false, false)
			}
			header = append(header, prefix+line)
		}
		return header, true
	}
	return nil, false
}

func terminalTeammateAttachmentSummary(text string) (string, bool) {
	kind, body := teamspawn.ParseInteractiveProtocol(text)
	if kind == "shutdown_request" {
		value := "[Shutdown Request from " + body.String("from") + "]"
		if reason := body.String("reason"); reason != "" {
			value += " " + reason
		}
		return value, true
	}
	body = presentationObject([]byte(text))
	if body.String("type") == "shutdown_rejected" && terminalTeammateStringFields(body, "requestId", "from", "reason", "timestamp") {
		return "[Shutdown Rejected] " + body.String("from") + ": " + body.String("reason"), true
	}
	return "", false
}

func terminalTeammateStringFields(body protocol.JSONObject, keys ...string) bool {
	for _, key := range keys {
		if _, err := protocol.DecodeJavaScriptJSONString(body.Get(key)); err != nil {
			return false
		}
	}
	return true
}
