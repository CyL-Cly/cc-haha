package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
)

type terminalTeammateSpinnerRow struct {
	idleStarted    time.Time
	frozenDuration string
	verb, pastVerb string
	render         func(time.Time) []string
	rows           []string
}

func terminalSpinnerNumber(value json.RawMessage) float64 {
	var n float64
	_ = json.Unmarshal(value, &n)
	return n
}
func terminalSpinnerDuration(milliseconds float64) string {
	if milliseconds < 60000 {
		return fmt.Sprintf("%ds", int(math.Floor(milliseconds/1000)))
	}
	minutes := int(math.Floor(milliseconds / 60000))
	seconds := int(math.Floor(math.Mod(milliseconds, 60000)/1000 + .5))
	if seconds == 60 {
		minutes++
		seconds = 0
	}
	return fmt.Sprintf("%dm %ds", minutes, seconds)
}

func terminalSpinnerActivity(task protocol.JSONObject, maxWidth int, fallback string) string {
	progress := presentationObject(task.Get("progress"))
	var activities []protocol.JSONObject
	_ = json.Unmarshal(progress.Get("recentActivities"), &activities)
	search, read := 0, 0
	for i := len(activities) - 1; i >= 0; i-- {
		if string(activities[i].Get("isSearch")) == "true" {
			search++
		} else if string(activities[i].Get("isRead")) == "true" {
			read++
		} else {
			break
		}
	}
	text := ""
	if search+read >= 2 {
		if search > 0 {
			noun := "patterns"
			if search == 1 {
				noun = "pattern"
			}
			text = fmt.Sprintf("Searching for %d %s", search, noun)
		}
		if read > 0 {
			noun := "files"
			if read == 1 {
				noun = "file"
			}
			prefix := "Reading"
			if text != "" {
				text += ", "
				prefix = "reading"
			}
			text += fmt.Sprintf("%s %d %s", prefix, read, noun)
		}
	} else {
		for i := len(activities) - 1; i >= 0; i-- {
			if value := activities[i].String("activityDescription"); value != "" {
				text = value
				break
			}
		}
	}
	if text == "" {
		text = presentationObject(progress.Get("lastActivity")).String("activityDescription")
	}
	if text == "" {
		return fallback
	}
	return ansi.Truncate(displayText(text), maxWidth, "…")
}

func terminalSpinnerMessagePreview(task protocol.JSONObject) []string {
	var messages []protocol.JSONObject
	_ = json.Unmarshal(task.Get("messages"), &messages)
	lines := []string{}
	for i := len(messages) - 1; i >= 0 && len(lines) < 3; i-- {
		m := messages[i]
		if m.String("type") != "assistant" && m.String("type") != "user" {
			continue
		}
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(presentationObject(m.Get("message")).Get("content"), &blocks)
		for _, block := range blocks {
			if len(lines) >= 3 {
				break
			}
			if block.String("type") == "tool_use" {
				input := presentationObject(block.Get("input"))
				line := "Using " + block.String("name") + "…"
				for _, field := range []string{"description", "prompt", "command", "query", "pattern"} {
					if value := input.String(field); value != "" {
						line = strings.Split(value, "\n")[0]
						break
					}
				}
				lines = append(lines, ansi.Truncate(displayText(line), 80, "…"))
			} else if block.String("type") == "text" {
				parts := strings.Split(block.String("text"), "\n")
				for j := len(parts) - 1; j >= 0 && len(lines) < 3; j-- {
					if strings.TrimSpace(parts[j]) != "" {
						lines = append(lines, ansi.Truncate(displayText(parts[j]), 80, "…"))
					}
				}
			}
		}
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}

func (s *State) terminalSpinnerTreeRows(now time.Time, theme, leaderVerb string, leaderTokens float64) []string {
	if s.Teammates == nil || s.Teammates.ExpandedView != "teammates" {
		s.clearTerminalSpinnerTree()
		return nil
	}
	tasks := s.Teammates.Running()
	if len(tasks) == 0 {
		s.clearTerminalSpinnerTree()
		return nil
	}
	r := s.ensureTerminalSpinnerClock().render
	if r.tree == nil {
		r.tree = map[string]*terminalTeammateSpinnerRow{}
	}
	allIdle := true
	live := map[string]bool{}
	for _, task := range tasks {
		allIdle = allIdle && string(task.Get("isIdle")) == "true"
		live[task.String("id")] = true
	}
	for id := range r.tree {
		if !live[id] {
			delete(r.tree, id)
		}
	}
	color := func(text, role string, bold bool) string { return terminalTaskText(text, theme, role, bold, false) }
	dim := func(text string) string { return color(text, "inactive", false) }
	row := func(parts ...string) []string {
		result := terminalTaskFlexRows(parts, max(1, s.Width-3))
		for i := range result {
			result[i] = "   " + result[i]
		}
		return result
	}
	selecting := s.Teammates.ViewSelectionMode == "selecting-agent"
	selected := s.Teammates.SelectedIPAgentIndex
	foreground := s.Teammates.ViewingAgentTaskID
	highlight := foreground == "" || selecting && selected == -1
	pointer, role, tree := " ", "", "┌─ "
	if selecting && selected == -1 {
		pointer, role = "❯", "suggestion"
	}
	if highlight {
		tree = "╒═ "
	}
	treeRole := ""
	if !highlight {
		treeRole = "inactive"
	}
	nameRole := "cyan_FOR_SUBAGENTS_ONLY"
	if role != "" {
		nameRole = role
	}
	parts := []string{color(pointer, role, highlight), color(tree, treeRole, highlight), color("team-lead", nameRole, highlight)}
	if foreground != "" {
		if leaderVerb != "" {
			parts = append(parts, dim(": "+leaderVerb+"…"))
		} else {
			parts = append(parts, dim(": Idle"))
		}
	}
	if leaderTokens > 0 {
		tokenText := terminalSpinnerFormatTokens(leaderTokens)
		if leaderTokens >= 1000 && leaderTokens < 10000 {
			tokenText = fmt.Sprintf("%.1fk", leaderTokens/1000)
		}
		tokenRole := ""
		if !highlight {
			tokenRole = "inactive"
		}
		parts = append(parts, color(" · "+tokenText+" tokens", tokenRole, false))
	}
	if highlight {
		parts = append(parts, dim(" · shift + ↑/↓ to select"))
	}
	if selecting && selected == -1 && foreground != "" {
		parts = append(parts, dim(" · enter to view"))
	}
	nextOrder := make([]string, len(tasks))
	for i, task := range tasks {
		nextOrder[i] = task.String("id")
	}
	structural := !slices.Equal(nextOrder, r.treeOrder) || selecting != r.treeHadHideRow
	sameLeader := r.treeLeaderWidth == s.Width && slices.Equal(parts, r.treeLeaderParts)
	if !sameLeader || structural || r.treeHeaderDirty {
		painted := terminalTaskFlexPaint(parts, max(1, s.Width-3), sameLeader)
		for i := range painted {
			painted[i] = "   " + painted[i]
		}
		r.treeLeaderRows = append([]string{""}, painted...)
	}
	r.treeLeaderParts, r.treeLeaderWidth = append([]string(nil), parts...), s.Width
	r.treeHadHideRow = selecting
	r.treeOrder = nil
	r.treeTailRows = nil
	for index, task := range tasks {
		id := task.String("id")
		state := r.tree[id]
		if state == nil {
			state = &terminalTeammateSpinnerRow{verb: task.String("spinnerVerb"), pastVerb: task.String("pastTenseVerb")}
			if state.verb == "" {
				state.verb = "Working"
			}
			if state.pastVerb == "" {
				state.pastVerb = "Worked"
			}
			r.tree[id] = state
		}
		idle := string(task.Get("isIdle")) == "true"
		if idle && state.idleStarted.IsZero() {
			state.idleStarted = now
		} else if !idle {
			state.idleStarted = time.Time{}
		}
		if !allIdle {
			state.frozenDuration = ""
		} else if state.frozenDuration == "" {
			state.frozenDuration = terminalSpinnerDuration(math.Max(0, float64(now.UnixMilli())-terminalSpinnerNumber(task.Get("startTime"))-terminalSpinnerNumber(task.Get("totalPausedMs"))))
		}
		state.render = func(at time.Time) []string {
			chosen := selecting && selected == index
			front := foreground == id
			highlight := chosen || front
			last := !selecting && index == len(tasks)-1
			tree := "├─ "
			if last {
				tree = "└─ "
			}
			if highlight {
				tree = "╞═ "
				if last {
					tree = "╘═ "
				}
			}
			pointer, role := " ", ""
			if chosen {
				pointer, role = "❯", "suggestion"
			}
			treeRole := "inactive"
			if chosen {
				treeRole = ""
			}
			parts := []string{color(pointer, role, chosen), color(tree, treeRole, false)}
			identity := presentationObject(task.Get("identity"))
			name := "@" + identity.String("agentName")
			showName := s.Width >= 60 && s.Width-8-exportWidth(name)-2 >= 25
			available := s.Width - 8
			if showName {
				available -= exportWidth(name) + 2
			}
			progress := presentationObject(task.Get("progress"))
			tools := int(terminalSpinnerNumber(progress.Get("toolUseCount")))
			tokens := terminalSpinnerNumber(progress.Get("tokenCount"))
			noun := "uses"
			if tools == 1 {
				noun = "use"
			}
			stats := fmt.Sprintf(" · %d tool %s · %s tokens", tools, noun, terminalSpinnerFormatTokens(tokens))
			viewHint := chosen && !front && available > exportWidth(" · enter to view")+exportWidth(stats)+25+5
			viewCost := 0
			if viewHint {
				viewCost = exportWidth(" · enter to view")
			}
			selectHint := highlight && available > exportWidth(" · shift + ↑/↓ to select")+viewCost+exportWidth(stats)+25+5
			showStats := available > exportWidth(stats)+25+5
			extras := 0
			if showStats {
				extras += exportWidth(stats)
			}
			if selectHint {
				extras += exportWidth(" · shift + ↑/↓ to select")
			}
			extras += viewCost
			if showName {
				nameRole := identity.String("color") + "_FOR_SUBAGENTS_ONLY"
				if identity.String("color") == "" {
					nameRole = "cyan_FOR_SUBAGENTS_ONLY"
				}
				if chosen {
					nameRole = "suggestion"
				}
				parts = append(parts, color(name, nameRole, false), color(": ", treeRole, false))
			}
			status, statusRole := "", "inactive"
			switch {
			case string(task.Get("shutdownRequested")) == "true":
				status = "[stopping]"
			case string(task.Get("awaitingPlanApproval")) == "true":
				status, statusRole = "[awaiting approval]", "warning"
			case idle:
				if allIdle {
					status = state.pastVerb + " for " + state.frozenDuration
				} else {
					status = "Idle for " + terminalSpinnerDuration(float64(at.Sub(state.idleStarted).Milliseconds()))
				}
			case !highlight:
				status = terminalSpinnerActivity(task, max(25, available-extras-1), state.verb)
				if !strings.HasSuffix(status, "…") {
					status += "…"
				}
			}
			if status != "" {
				parts = append(parts, color(status, statusRole, false))
			}
			if showStats {
				parts = append(parts, dim(stats))
			}
			if selectHint {
				parts = append(parts, dim(" · shift + ↑/↓ to select"))
			}
			if viewHint {
				parts = append(parts, dim(" · enter to view"))
			}
			rendered := row(parts...)
			if s.ShowTeammatePreview {
				prefix := "│   "
				if last {
					prefix = "    "
				}
				for _, text := range terminalSpinnerMessagePreview(task) {
					rendered = append(rendered, row(dim(" "), dim(prefix), dim(text))...)
				}
			}
			return rendered
		}
		state.rows = state.render(now)
		r.treeOrder = append(r.treeOrder, id)
	}
	if selecting {
		chosen := selected == len(tasks)
		pointer, tree := " ", "└─ "
		if chosen {
			pointer, tree = "❯", "╘═ "
		}
		pointerRole := ""
		textRole := "inactive"
		if chosen {
			pointerRole = "suggestion"
			textRole = ""
		}
		parts := []string{color(pointer, pointerRole, chosen), color(tree, textRole, chosen), color("hide", textRole, chosen)}
		if chosen {
			parts = append(parts, dim(" · enter to collapse"))
		}
		r.treeTailRows = row(parts...)
	}
	return r.composeTerminalSpinnerTree()
}
