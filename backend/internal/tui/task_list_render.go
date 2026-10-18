package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sourcecollation"
	"github.com/charmbracelet/x/ansi"
)

// These refs belong to the mounted TaskListV2, independently of the always
// mounted filesystem store. A menu or standalone/spinner transition unmounts
// the component, so an already completed task is not a recent completion on
// its next mount.
type terminalTaskListRenderState struct {
	previous  map[string]bool
	completed map[string]time.Time
}

func terminalTaskIdentity(task protocol.JSONObject) string {
	units, err := protocol.DecodeJavaScriptJSONString(task.Get("id"))
	if err != nil {
		return ""
	}
	return string(protocol.QuoteJavaScriptUTF16(units))
}

func (r *terminalTaskListRenderState) observe(tasks []protocol.JSONObject, now time.Time) {
	current := make(map[string]bool)
	for _, task := range tasks {
		if task.String("status") == "completed" {
			current[terminalTaskIdentity(task)] = true
		}
	}
	if r.completed == nil {
		r.completed = make(map[string]time.Time)
	}
	if r.previous != nil {
		for id := range current {
			if !r.previous[id] {
				r.completed[id] = now
			}
		}
	}
	for id := range r.completed {
		if !current[id] {
			delete(r.completed, id)
		}
	}
	r.previous = current
}

func terminalTaskDecimalPrefix(value string) (float64, bool) {
	value = strings.TrimLeftFunc(value, protocol.IsJavaScriptWhitespace)
	end := 0
	if len(value) > 0 && (value[0] == '-' || value[0] == '+') {
		end++
	}
	start := end
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == start {
		return 0, false
	}
	result, err := strconv.ParseFloat(value[:end], 64)
	if failure, ok := err.(*strconv.NumError); ok && failure.Err == strconv.ErrRange {
		return result, true
	}
	return result, err == nil
}

func terminalTaskIDLess(a, b protocol.JSONObject, compare *sourcecollation.Comparator) bool {
	an, aok := terminalTaskDecimalPrefix(a.String("id"))
	bn, bok := terminalTaskDecimalPrefix(b.String("id"))
	if aok && bok {
		return an < bn
	}
	au, _ := protocol.DecodeJavaScriptJSONString(a.Get("id"))
	bu, _ := protocol.DecodeJavaScriptJSONString(b.Get("id"))
	if compare != nil {
		return compare.Compare(au, bu) < 0
	}
	return a.String("id") < b.String("id")
}

func terminalTaskBlockedBy(task protocol.JSONObject, unresolved map[string]bool) []string {
	var ids []json.RawMessage
	_ = json.Unmarshal(task.Get("blockedBy"), &ids)
	result := []string{}
	for _, id := range ids {
		units, err := protocol.DecodeJavaScriptJSONString(id)
		if err == nil && unresolved[string(protocol.QuoteJavaScriptUTF16(units))] {
			var label string
			_ = json.Unmarshal(id, &label)
			result = append(result, label)
		}
	}
	return result
}

func terminalTaskText(value, theme, role string, bold, strike bool) string {
	prefix := TerminalThemeRoleEscape(theme, role, false)
	if bold {
		prefix += "\x1b[1m"
	}
	if strike {
		prefix += "\x1b[9m"
	}
	if prefix == "" {
		return displayText(value)
	}
	return prefix + displayText(value) + "\x1b[0m"
}

func terminalTaskFlexRows(parts []string, width int) []string {
	return terminalTaskFlexPaint(parts, width, false)
}

func terminalTaskFlexPaint(parts []string, width int, cachedOrigins bool) []string {
	rows := terminalFooterFlexPaint(parts, width, nil, cachedOrigins)
	bases, total := make([]int, len(parts)), 0
	for i, part := range parts {
		for _, line := range exportWrapWords(ansi.Strip(part), width) {
			bases[i] = max(bases[i], exportWidth(line))
		}
		total += bases[i]
	}
	height := 0
	for i, part := range parts {
		allocation := float64(bases[i])
		if total > width {
			allocation *= float64(width) / float64(total)
		}
		// Yoga's measure uses its fractional AtMost constraint. The painter
		// subsequently uses rounded edges and may paint fewer rows; the
		// measured height remains reserved for the next TaskItem sibling.
		height = max(height, len(exportWrapWords(ansi.Strip(part), max(1, int(math.Floor(allocation))))))
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return rows
}

func terminalTaskActivity(task protocol.JSONObject) string {
	progress := presentationObject(task.Get("progress"))
	var recent []protocol.JSONObject
	_ = json.Unmarshal(progress.Get("recentActivities"), &recent)
	searches, reads := 0, 0
	for i := len(recent) - 1; i >= 0; i-- {
		if string(recent[i].Get("isSearch")) == "true" {
			searches++
		} else if string(recent[i].Get("isRead")) == "true" {
			reads++
		} else {
			break
		}
	}
	if searches+reads >= 2 {
		parts := []string{}
		if searches > 0 {
			word := "patterns"
			if searches == 1 {
				word = "pattern"
			}
			parts = append(parts, fmt.Sprintf("Searching for %d %s", searches, word))
		}
		if reads > 0 {
			verb, word := "reading", "files"
			if len(parts) == 0 {
				verb = "Reading"
			}
			if reads == 1 {
				word = "file"
			}
			parts = append(parts, fmt.Sprintf("%s %d %s", verb, reads, word))
		}
		return strings.Join(parts, ", ") + "…"
	}
	for i := len(recent) - 1; i >= 0; i-- {
		if description := recent[i].String("activityDescription"); description != "" {
			return description
		}
	}
	return presentationObject(progress.Get("lastActivity")).String("activityDescription")
}

func (r *terminalTaskListRenderState) rows(tasks []protocol.JSONObject, background, teamContext protocol.JSONObject, teamsEnabled, standalone bool, width, height int, theme string, now time.Time, contentWidth ...int) []string {
	if len(tasks) == 0 {
		return nil
	}
	r.observe(tasks, now)
	compare, _ := sourcecollation.New()
	if compare != nil {
		defer compare.Close()
	}
	unresolved := map[string]bool{}
	completed, pending := 0, 0
	for _, task := range tasks {
		switch task.String("status") {
		case "completed":
			completed++
		case "pending":
			pending++
		}
		if task.String("status") != "completed" {
			unresolved[terminalTaskIdentity(task)] = true
		}
	}
	maximum := 0
	if height > 10 {
		maximum = min(10, max(3, height-14))
	}
	visible := append([]protocol.JSONObject(nil), tasks...)
	less := func(a, b protocol.JSONObject) bool { return terminalTaskIDLess(a, b, compare) }
	if len(tasks) > maximum {
		priority := func(task protocol.JSONObject) int {
			switch task.String("status") {
			case "completed":
				if at, ok := r.completed[terminalTaskIdentity(task)]; ok && now.Sub(at) < 30*time.Second {
					return 0
				}
				return 4
			case "in_progress":
				return 1
			default:
				if len(terminalTaskBlockedBy(task, unresolved)) > 0 {
					return 3
				}
				return 2
			}
		}
		sort.SliceStable(visible, func(i, j int) bool {
			a, b := priority(visible[i]), priority(visible[j])
			return a < b || a == b && less(visible[i], visible[j])
		})
	} else {
		sort.SliceStable(visible, func(i, j int) bool { return less(visible[i], visible[j]) })
	}
	hidden := visible[min(len(visible), maximum):]
	visible = visible[:min(len(visible), maximum)]
	colors, activity, active := map[string]string{}, map[string]string{}, map[string]bool{}
	if teamsEnabled {
		for _, item := range presentationObject(teamContext.Get("teammates")).OwnProperties() {
			member := presentationObject(item.Value)
			if color := member.String("color"); color != "" {
				switch color {
				case "red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan":
					colors[member.String("name")] = color + "_FOR_SUBAGENTS_ONLY"
				}
			}
		}
		for _, item := range background.OwnProperties() {
			task := presentationObject(item.Value)
			if task.String("type") != "in_process_teammate" || task.String("status") != "running" {
				continue
			}
			identity := presentationObject(task.Get("identity"))
			for _, name := range []string{identity.String("agentName"), identity.String("agentId")} {
				active[name] = true
				activity[name] = terminalTaskActivity(task)
			}
		}
	}
	indent, available := "", max(1, width)
	if len(contentWidth) > 0 {
		available = max(1, contentWidth[0])
	}
	rows := []string{}
	if standalone {
		indent, available = "  ", max(1, width-2)
		progress := len(tasks) - completed - pending
		header := terminalTaskText(strconv.Itoa(len(tasks)), theme, "inactive", true, false) + terminalTaskText(" tasks (", theme, "inactive", false, false) + terminalTaskText(strconv.Itoa(completed), theme, "inactive", true, false) + terminalTaskText(" done, ", theme, "inactive", false, false)
		if progress > 0 {
			header += terminalTaskText(strconv.Itoa(progress), theme, "inactive", true, false) + terminalTaskText(" in progress, ", theme, "inactive", false, false)
		}
		header += terminalTaskText(strconv.Itoa(pending), theme, "inactive", true, false) + terminalTaskText(" open)", theme, "inactive", false, false)
		rows = append(rows, "")
		for _, line := range terminalFooterFlexRow([]string{header}, available) {
			rows = append(rows, indent+line)
		}
	}
	for _, task := range visible {
		status, owner := task.String("status"), task.String("owner")
		blockers := terminalTaskBlockedBy(task, unresolved)
		showOwner := width >= 60 && owner != "" && active[owner]
		ownerWidth := 0
		if showOwner {
			ownerWidth = exportWidth(" (@" + owner + ")")
		}
		icon, role := "◻", ""
		if status == "in_progress" {
			icon, role = "◼", "claude"
		} else if status == "completed" {
			icon, role = "✔", "success"
		}
		subjectRole := ""
		if status == "completed" || len(blockers) > 0 {
			subjectRole = "inactive"
		}
		parts := []string{terminalTaskText(icon+" ", theme, role, false, false), terminalTaskText(exportTruncate(task.String("subject"), max(15, width-15-ownerWidth), true), theme, subjectRole, status == "in_progress", status == "completed")}
		if showOwner {
			ownerRole := colors[owner]
			if ownerRole == "" {
				ownerRole = "inactive"
			}
			parts = append(parts, terminalTaskText(" (", theme, "inactive", false, false)+terminalTaskText("@"+owner, theme, ownerRole, false, false)+terminalTaskText(")", theme, "inactive", false, false))
		}
		if len(blockers) > 0 {
			sort.SliceStable(blockers, func(i, j int) bool {
				a, _ := terminalTaskDecimalPrefix(blockers[i])
				b, _ := terminalTaskDecimalPrefix(blockers[j])
				return a < b
			})
			labels := make([]string, len(blockers))
			for i, id := range blockers {
				labels[i] = "#" + id
			}
			parts = append(parts, terminalTaskText(" › blocked by "+strings.Join(labels, ", "), theme, "inactive", false, false))
		}
		for _, line := range terminalTaskFlexRows(parts, available) {
			rows = append(rows, indent+line)
		}
		if status == "in_progress" && len(blockers) == 0 && activity[owner] != "" {
			value := terminalTaskText("  "+exportTruncate(activity[owner], max(15, width-15), true)+"…", theme, "inactive", false, false)
			for _, line := range terminalFooterFlexRow([]string{value}, available) {
				rows = append(rows, indent+line)
			}
		}
	}
	if len(hidden) > 0 && maximum > 0 {
		counts := map[string]int{}
		for _, task := range hidden {
			counts[task.String("status")]++
		}
		parts := []string{}
		for _, item := range []struct{ key, label string }{{"in_progress", "in progress"}, {"pending", "pending"}, {"completed", "completed"}} {
			if counts[item.key] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[item.key], item.label))
			}
		}
		for _, line := range terminalFooterFlexRow([]string{terminalTaskText(" … +"+strings.Join(parts, ", "), theme, "inactive", false, false)}, available) {
			rows = append(rows, indent+line)
		}
	}
	return rows
}
