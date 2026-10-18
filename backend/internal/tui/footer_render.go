package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sourcecollation"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

func terminalTaskPillLabel(tasks []protocol.JSONObject) string {
	if len(tasks) == 0 {
		return ""
	}
	kind, same := tasks[0].String("type"), true
	for _, task := range tasks {
		same = same && task.String("type") == kind
	}
	n := len(tasks)
	plural := func(singular, multiple string) string {
		if n == 1 {
			return "1 " + singular
		}
		return fmt.Sprintf("%d %s", n, multiple)
	}
	if same {
		switch kind {
		case "local_bash":
			monitors := 0
			for _, task := range tasks {
				if task.String("kind") == "monitor" {
					monitors++
				}
			}
			parts := []string{}
			if shells := n - monitors; shells > 0 {
				parts = append(parts, fmt.Sprintf("%d shell%s", shells, map[bool]string{true: "", false: "s"}[shells == 1]))
			}
			if monitors > 0 {
				parts = append(parts, fmt.Sprintf("%d monitor%s", monitors, map[bool]string{true: "", false: "s"}[monitors == 1]))
			}
			return strings.Join(parts, ", ")
		case "in_process_teammate":
			teams := map[string]bool{}
			for _, task := range tasks {
				id := presentationObject(task.Get("identity"))
				teams[string(id.Get("teamName"))] = true
			}
			if len(teams) == 1 {
				return "1 team"
			}
			return fmt.Sprintf("%d teams", len(teams))
		case "local_agent":
			return plural("local agent", "local agents")
		case "remote_agent":
			if n == 1 && string(tasks[0].Get("isUltraplan")) == "true" {
				switch tasks[0].String("ultraplanPhase") {
				case "plan_ready":
					return "◆ ultraplan ready"
				case "needs_input":
					return "◇ ultraplan needs your input"
				default:
					return "◇ ultraplan"
				}
			}
			return "◇ " + plural("cloud session", "cloud sessions")
		case "local_workflow":
			return plural("background workflow", "background workflows")
		case "monitor_mcp":
			return plural("monitor", "monitors")
		case "dream":
			return "dreaming"
		}
	}
	return fmt.Sprintf("%d background %s", n, map[bool]string{true: "task", false: "tasks"}[n == 1])
}

func terminalPillStyle(text, theme, role string, selected, bold bool) string {
	value := TerminalThemeRoleEscape(theme, role, false)
	if selected {
		value += "\x1b[7m"
	}
	if bold {
		value += "\x1b[1m"
	}
	return value + displayText(text) + "\x1b[0m"
}

func terminalFooterScrollWindow(widths []int, width, selected int) (int, int, bool, bool) {
	n := len(widths)
	if n == 0 {
		return 0, 0, false, false
	}
	selected = min(max(0, selected), n-1)
	cumulative := []int{0}
	for _, value := range widths {
		cumulative = append(cumulative, cumulative[len(cumulative)-1]+value)
	}
	if cumulative[n] <= width {
		return 0, n, false, false
	}
	rangeWidth := func(start, end int) int {
		v := cumulative[end] - cumulative[start]
		if start > 0 {
			v--
		}
		return v
	}
	effective := func(start, end int) int {
		v := width
		if start > 0 {
			v -= 2
		}
		if end < n {
			v -= 2
		}
		return v
	}
	start, end := 0, 1
	for end < n && rangeWidth(start, end+1) <= effective(start, end+1) {
		end++
	}
	if selected >= end {
		end = selected + 1
		start = selected
		for start > 0 && rangeWidth(start-1, end) <= effective(start-1, end) {
			start--
		}
	}
	return start, end, start > 0, end < n
}

func terminalFooterFlexRow(parts []string, width int, boxes ...[]bool) []string {
	return terminalFooterFlexPaint(parts, width, boxes, false)
}

// A clean Yoga subtree can reuse rounded origins while restoring its cached
// unrounded measure dimensions. This changes the far edge of fractional Text
// allocations when a later structural change forces a fresh paint.
func terminalFooterFlexPaint(parts []string, width int, boxes [][]bool, cachedOrigins bool) []string {
	width = max(1, width)
	total := 0
	bases := make([]int, len(parts))
	for i, part := range parts {
		for _, row := range exportWrapWords(ansi.Strip(part), width) {
			bases[i] = max(bases[i], uniseg.StringWidth(row))
		}
		total += bases[i]
	}
	grid := [][]*terminalStatusLineCell{}
	origin := float64(0)
	for index, part := range parts {
		basis := float64(bases[index])
		allocated := basis
		if total > width {
			allocated *= float64(width) / float64(total)
		}
		left := int(math.Floor(origin))
		end := int(math.Ceil(origin + allocated))
		if cachedOrigins {
			end = left + int(math.Ceil(allocated))
		}
		isBox := len(boxes) > 0 && index < len(boxes[0]) && boxes[0][index]
		if isBox {
			left = int(math.Floor(origin + 0.5))
			end = int(math.Floor(origin + allocated + 0.5))
		}
		origin += allocated
		plain := ansi.Strip(part)
		leafWidth := max(1, end-left)
		if isBox {
			// Yoga rounds the Box's edges, but its inner Text retains the
			// narrower AtMost measurement. Bun.wrapAnsi floors that constraint.
			leafWidth = 1
			for _, row := range exportWrapWords(plain, max(1, int(math.Floor(allocated)))) {
				leafWidth = max(leafWidth, uniseg.StringWidth(row))
			}
		}
		wrapped := exportWrapWords(plain, leafWidth)
		painted := terminalMarkdownApplyStyles(part, wrapped)
		for y, line := range painted {
			for len(grid) <= y {
				grid = append(grid, make([]*terminalStatusLineCell, width))
			}
			x := left
			for _, cell := range terminalStatusLineParse(line, false, "39") {
				n := uniseg.StringWidth(cell.text)
				if x >= 0 && x+n <= width {
					c := cell
					grid[y][x] = &c
					for j := 1; j < n; j++ {
						grid[y][x+j] = &terminalStatusLineCell{text: "", style: cell.style}
					}
				}
				x += n
			}
		}
	}
	rows := []string{}
	for _, line := range grid {
		end := len(line)
		for end > 0 && line[end-1] == nil {
			end--
		}
		row := strings.Builder{}
		for _, cell := range line[:end] {
			if cell == nil {
				row.WriteByte(' ')
			} else if cell.text != "" {
				if cell.style.sgr() == "39" {
					row.WriteString(cell.text)
				} else {
					row.WriteString("\x1b[0;" + cell.style.sgr() + "m" + cell.text + "\x1b[0m")
				}
			}
		}
		rows = append(rows, row.String())
	}
	return rows
}

func (s *State) terminalTasksPillLines() []string {
	tasks := []protocol.JSONObject{}
	for _, property := range s.BackgroundTasks.OwnProperties() {
		task := presentationObject(property.Value)
		if terminalVisibleBackgroundTask(task) {
			tasks = append(tasks, task)
		}
	}
	if len(tasks) == 0 {
		return nil
	}
	showTree := s.Teammates != nil && s.Teammates.ExpandedView == "teammates"
	allIP := !showTree
	for _, task := range tasks {
		allIP = allIP && task.String("type") == "in_process_teammate"
	}
	viewed := s.Teammates != nil && s.Teammates.ViewSelectionMode == "viewing-agent"
	if viewed {
		task, ok := s.BackgroundTasks.Object(s.Teammates.ViewingAgentTaskID)
		viewed = ok && task.String("type") == "in_process_teammate"
	}
	theme := s.previewTheme()
	selected := s.selectedTerminalFooter() == "tasks"
	if allIP || !showTree && viewed {
		members := []protocol.JSONObject{}
		for _, task := range tasks {
			if task.String("type") == "in_process_teammate" {
				members = append(members, task)
			}
		}
		if comparator, err := sourcecollation.New(); err == nil {
			defer comparator.Close()
			sort.SliceStable(members, func(i, j int) bool {
				a := presentationObject(members[i].Get("identity"))
				b := presentationObject(members[j].Get("identity"))
				au, _ := protocol.DecodeJavaScriptJSONString(a.Get("agentName"))
				bu, _ := protocol.DecodeJavaScriptJSONString(b.Get("agentName"))
				return comparator.Compare(au, bu) < 0
			})
		}
		if !selected {
			sort.SliceStable(members, func(i, j int) bool {
				a, b := members[i].Get("isIdle"), members[j].Get("isIdle")
				return string(a) != string(b) && string(a) != "true"
			})
		}
		labels := []string{"@main"}
		for _, task := range members {
			id := presentationObject(task.Get("identity"))
			labels = append(labels, "@"+displayText(id.String("agentName")))
		}
		widths := []int{}
		for i, label := range labels {
			widths = append(widths, uniseg.StringWidth(label)+min(i, 1))
		}
		index := 0
		if selected {
			index = s.footerTeammateIndex
		}
		start, end, left, right := terminalFooterScrollWindow(widths, max(20, s.Width-24), index)
		parts := []string{}
		boxes := []bool{}
		if left {
			parts = append(parts, terminalPillStyle("← ", theme, "inactive", false, false))
			boxes = append(boxes, false)
		}
		for i := start; i < end; i++ {
			if i > start {
				parts = append(parts, " ")
				boxes = append(boxes, false)
			}
			role, idle, bold := "", !s.Busy(), !viewed && i == 0
			if i > 0 {
				task := members[i-1]
				identity := presentationObject(task.Get("identity"))
				color := identity.String("color")
				if terminalFooterThemeRoles["dark"][color+"_FOR_SUBAGENTS_ONLY"] != "" {
					role = color + "_FOR_SUBAGENTS_ONLY"
				}
				idle = string(task.Get("isIdle")) == "true"
				bold = viewed && task.String("id") == s.Teammates.ViewingAgentTaskID
			}
			if selected && s.footerTeammateIndex == i {
				if role != "" {
					parts = append(parts, TerminalThemeRoleEscape(theme, role, true)+terminalPillStyle(labels[i], theme, "inverseText", false, bold))
				} else {
					parts = append(parts, terminalPillStyle(labels[i], theme, "background", true, bold))
				}
			} else {
				if idle || role == "" && !bold {
					role = "inactive"
				}
				parts = append(parts, terminalPillStyle(labels[i], theme, role, false, bold))
			}
			boxes = append(boxes, true)
		}
		if right {
			parts = append(parts, terminalPillStyle(" →", theme, "inactive", false, false))
			boxes = append(boxes, false)
		}
		parts = append(parts, terminalPillStyle(" · shift + ↓ to expand", theme, "inactive", false, false))
		boxes = append(boxes, false)
		return terminalFooterFlexRow(parts, s.Width, boxes)
	}
	if !s.terminalTasksFooterVisible() {
		return nil
	}
	parts := []string{terminalPillStyle(terminalTaskPillLabel(tasks), theme, "background", selected, false)}
	if len(tasks) == 1 && tasks[0].String("type") == "remote_agent" && string(tasks[0].Get("isUltraplan")) == "true" && len(tasks[0].Get("ultraplanPhase")) > 0 {
		parts = append(parts, terminalPillStyle(" · ↓ to view", theme, "inactive", false, false))
	}
	rows := terminalFooterFlexRow(parts, s.Width, []bool{true, false})
	if len(rows) > 0 {
		return rows[:1]
	}
	return nil
}

func (s *State) terminalTeamsPillLines() []string {
	if len(s.CachedTeams) == 0 {
		return nil
	}
	var count int
	_ = json.Unmarshal(s.CachedTeams[0].Get("memberCount"), &count)
	if count == 0 {
		return nil
	}
	label := fmt.Sprintf("%d teammate%s", count, map[bool]string{true: "", false: "s"}[count == 1])
	selected := s.selectedTerminalFooter() == "teams"
	line := terminalPillStyle(label, s.previewTheme(), "background", selected, false)
	if selected {
		line += " " + terminalPillStyle("· Enter to view", s.previewTheme(), "inactive", false, false)
	}
	return []string{line}
}

func (s *State) terminalNavigationFooterLines() []string {
	if s.terminalFooterOverlayActive() {
		return nil
	}
	tasks, teams := s.terminalTasksPillLines(), s.terminalTeamsPillLines()
	if len(tasks) > 1 {
		return append(tasks, teams...)
	}
	parts := append(tasks, teams...)
	if len(parts) == 0 {
		return nil
	}
	separator := terminalPillStyle(" · ", s.previewTheme(), "inactive", false, false)
	return []string{ansi.Truncate(strings.Join(parts, separator), max(1, s.Width), "")}
}
