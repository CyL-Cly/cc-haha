package tui

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// These are SpinnerWithVerb's mount-owned refs, separate from REPL's turn
// clock and response length. Local JSX can unmount them while the turn lives.
type terminalSpinnerRenderState struct {
	tree                  map[string]*terminalTeammateSpinnerRow
	treeRows              []string
	treeOrder             []string
	treeLeaderRows        []string
	treeTailRows          []string
	treeLeaderParts       []string
	treeLeaderWidth       int
	treeHadHideRow        bool
	treeHeaderRows        []string
	treeHeaderDirty       bool
	parentRevision        uint64
	childRevision         uint64
	renderedChildRevision uint64
	rows                  []string
	animation             terminalSpinnerAnimationState
	lastAnimationUpdate   time.Duration
	lastElapsedUpdate     time.Time
	elapsedSnapshot       time.Duration
	responseSnapshot      int
	previousMode          string
	thinkingStarted       time.Time
	thinking              bool
	thoughtFor            *time.Duration
	showThoughtAt         time.Time
	clearThoughtAt        time.Time
}

func (r *terminalSpinnerRenderState) observe(mode string, now time.Time) {
	if mode != r.previousMode {
		// The original effect's cleanup cancels both pending timeouts on
		// every mode change, including changes between non-thinking modes.
		r.showThoughtAt, r.clearThoughtAt = time.Time{}, time.Time{}
		if mode == "thinking" {
			if r.thinkingStarted.IsZero() {
				r.thinkingStarted = now
				r.thinking, r.thoughtFor = true, nil
			}
		} else if !r.thinkingStarted.IsZero() {
			duration := now.Sub(r.thinkingStarted)
			r.thoughtFor = &duration
			remaining := 2*time.Second - duration
			r.thinkingStarted = time.Time{}
			if remaining > 0 {
				r.showThoughtAt = now.Add(remaining)
			} else {
				r.thinking = false
				r.clearThoughtAt = now.Add(2 * time.Second)
			}
		}
		r.previousMode = mode
	}
	if !r.showThoughtAt.IsZero() && !now.Before(r.showThoughtAt) {
		r.showThoughtAt = time.Time{}
		r.thinking = false
		r.clearThoughtAt = now.Add(2 * time.Second)
	}
	if !r.clearThoughtAt.IsZero() && !now.Before(r.clearThoughtAt) {
		r.clearThoughtAt = time.Time{}
		r.thoughtFor = nil
	}
}

func (s *State) terminalSpinnerMounted() bool {
	return !s.taskListLocalJSXMounted() && len(s.Permissions) == 0 && s.question == nil &&
		!s.Closing && !s.taskListStreaming && (s.Busy() || len(s.Queue) > 0 || s.Teammates != nil && len(s.Teammates.Running()) > 0)
}

func (s *State) terminalSpinnerLines(now time.Time) []string {
	clock := s.ensureTerminalSpinnerClock()
	if !s.terminalSpinnerMounted() {
		clock.render = nil
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	if clock.render == nil {
		clock.render = &terminalSpinnerRenderState{}
	}
	r := clock.render
	parentChanged := r.rows == nil || r.parentRevision != clock.parentRevision
	if parentChanged {
		r.observe(clock.mode, now)
		r.elapsedSnapshot = clock.elapsed(now)
		r.lastElapsedUpdate = now
		r.responseSnapshot = clock.responseUnits
		r.animation.render(clock.responseUnits, clock.animationTime, s.Preferences.ReducedMotion, s.terminalSpinnerActiveTools())
		r.parentRevision = clock.parentRevision
		r.childRevision++
	}
	if r.rows != nil && r.renderedChildRevision == r.childRevision {
		return append([]string(nil), r.rows...)
	}
	theme := s.previewTheme()
	if theme == "auto" {
		theme = "dark"
		if s.LightBackground {
			theme = "light"
		}
	}
	verb := clock.verb
	if verb == "" && !clock.verbKnown {
		verb = "Working"
	}
	for _, task := range s.TaskItems {
		if task.String("status") != "pending" && task.String("status") != "completed" {
			verb = task.String("subject")
			if task.Get("activeForm") != nil && string(task.Get("activeForm")) != "null" {
				verb = task.String("activeForm")
			}
			break
		}
	}
	if clock.overrideMessage != nil {
		verb = *clock.overrideMessage
	}
	leaderVerb := verb
	foreground := protocol.NewJSONObject()
	if s.Teammates != nil && s.Teammates.ViewingAgentTaskID != "" {
		foreground, _ = s.Teammates.Tasks.Object(s.Teammates.ViewingAgentTaskID)
		if string(foreground.Get("isIdle")) != "true" && foreground.String("spinnerVerb") != "" {
			verb = foreground.String("spinnerVerb")
		}
	}
	message := displayText(verb) + "…"
	role := "claude"
	if clock.overrideColor != "" {
		role = clock.overrideColor
	}
	dim := func(value string) string { return terminalTaskText(value, theme, "inactive", false, false) }
	elapsed := float64(r.elapsedSnapshot.Milliseconds())
	timer := dreamDuration(elapsed)
	leaderTokens := math.Floor(float64(r.animation.displayedResponse)/4 + .5)
	tokens := terminalSpinnerFormatTokens(leaderTokens)
	tokenText := tokens + " tokens"
	runningTeams := s.Teammates != nil && len(s.Teammates.Running()) > 0
	arrow := "↓"
	if clock.mode == "requesting" {
		arrow = "↑"
	}
	thinking := ""
	if r.thinking {
		thinking = "thinking"
	} else if r.thoughtFor != nil {
		thinking = "thought for " + strconv.Itoa(max(1, int(math.Floor(r.thoughtFor.Seconds()+.5)))) + "s"
	}
	available := s.Width - exportWidth(message) - 2 - 5
	showThinking := thinking != "" && available > exportWidth(thinking)
	used := 0
	if showThinking {
		used = exportWidth(thinking) + 3
	}
	wantsStats := s.Preferences.Verbose || runningTeams || elapsed > 30000
	showTimer := wantsStats && available > used+exportWidth(timer)
	if showTimer {
		used += exportWidth(timer) + 3
	}
	showTokens := wantsStats && leaderTokens > 0 && available > used+exportWidth(tokenText)+2
	shimmer := "claudeShimmer"
	if clock.overrideColor == "claudeBlue_FOR_SYSTEM_SPINNER" {
		shimmer = "claudeBlueShimmer_FOR_SYSTEM_SPINNER"
	}
	glyph := r.animation.glyph([]string{"·", "✢", "✳", "✶", "✻", "✽"}, theme, role, s.Preferences.ReducedMotion, clock.overrideMessage != nil)
	parts := []terminalSpinnerFlexPart{{value: glyph, width: 2, fixed: true}}
	parts = append(parts, r.animation.messageParts(message, clock.mode, theme, role, shimmer, s.Preferences.ReducedMotion, clock.overrideMessage != nil)...)
	status := []terminalSpinnerFlexPart{}
	add := func(part terminalSpinnerFlexPart) {
		if len(status) > 0 {
			status = append(status, terminalSpinnerFlexPart{value: dim(" · ")})
		}
		status = append(status, part)
	}
	if clock.suffix != "" {
		add(terminalSpinnerFlexPart{value: dim(clock.suffix)})
	}
	if showTimer {
		add(terminalSpinnerFlexPart{value: dim(timer)})
	}
	if showTokens {
		tokenParts := dim(arrow) + " " + dim(tokenText)
		if runningTeams {
			tokenParts = dim(tokenText)
		}
		add(terminalSpinnerFlexPart{value: tokenParts})
	}
	if showThinking {
		value := dim(thinking)
		if r.thinking && !s.Preferences.ReducedMotion {
			value = terminalSpinnerColored(thinking, r.animation.thinkingColor())
			if clock.suffix == "" && !showTimer && !showTokens {
				value = terminalSpinnerColored("("+thinking+")", r.animation.thinkingColor())
			}
		}
		add(terminalSpinnerFlexPart{value: value})
	}
	if foreground.String("id") != "" && string(foreground.Get("isIdle")) != "true" {
		identity := presentationObject(foreground.Get("identity"))
		nameRole := identity.String("color") + "_FOR_SUBAGENTS_ONLY"
		parts = append(parts, terminalSpinnerFlexPart{value: dim("(esc to interrupt ")}, terminalSpinnerFlexPart{value: terminalTaskText(identity.String("agentName"), theme, nameRole, false, false)}, terminalSpinnerFlexPart{value: dim(")")})
	} else if len(status) > 0 {
		thinkingOnly := showThinking && r.thinking && clock.suffix == "" && !showTimer && !showTokens
		if !thinkingOnly {
			parts = append(parts, terminalSpinnerFlexPart{value: dim("(")})
		}
		parts = append(parts, status...)
		if !thinkingOnly {
			parts = append(parts, terminalSpinnerFlexPart{value: dim(")")})
		}
	}
	rows := append([]string{""}, terminalSpinnerFlexWrap(parts, s.Width)...)
	if runningTeams && (!s.Busy() && foreground.String("id") == "" || string(foreground.Get("isIdle")) == "true") {
		idle := "✻ Idle"
		allIdle := true
		for _, task := range s.Teammates.Running() {
			allIdle = allIdle && string(task.Get("isIdle")) == "true"
		}
		if !allIdle && foreground.String("id") == "" {
			idle += " · teammates running"
		}
		if allIdle && foreground.String("id") != "" {
			idle = "✻ Worked for " + terminalSpinnerDuration(float64(now.UnixMilli())-terminalSpinnerNumber(foreground.Get("startTime")))
		}
		rows = append([]string{""}, terminalTaskFlexRows([]string{dim(idle)}, s.Width)...)
	}
	if parentChanged {
		r.treeHeaderDirty = !slices.Equal(r.treeHeaderRows, rows)
		r.treeHeaderRows = append([]string(nil), rows...)
		treeVerb := leaderVerb
		if !s.Busy() {
			treeVerb = ""
		}
		r.treeRows = s.terminalSpinnerTreeRows(now, theme, treeVerb, math.Floor(float64(r.responseSnapshot)/4+.5))
	}
	if len(r.treeRows) > 0 {
		rows = append(rows, r.treeRows...)
	}
	if len(r.treeRows) > 0 {
	} else if s.terminalTaskListMount() == "spinner" {
		rows = append(rows, s.expandedTaskLines()...)
	} else if next, ok := terminalSpinnerNextTask(s.TaskItems); ok {
		rows = append(rows, terminalSpinnerResponseLines("Next: "+next.String("subject"), s.Width, theme)...)
	} else if s.Preferences.SpinnerTip != "" {
		rows = append(rows, terminalSpinnerResponseLines("Tip: "+s.Preferences.SpinnerTip, s.Width, theme)...)
	}
	r.rows = append([]string(nil), rows...)
	r.renderedChildRevision = r.childRevision
	return rows
}

func terminalSpinnerNextTask(tasks []protocol.JSONObject) (protocol.JSONObject, bool) {
	unresolved := map[string]bool{}
	for _, task := range tasks {
		if task.String("status") != "completed" {
			unresolved[terminalTaskIdentity(task)] = true
		}
	}
	var fallback *protocol.JSONObject
	for _, task := range tasks {
		if task.String("status") != "pending" {
			continue
		}
		if fallback == nil {
			copy := task
			fallback = &copy
		}
		blocked := false
		var ids []json.RawMessage
		_ = json.Unmarshal(task.Get("blockedBy"), &ids)
		for _, id := range ids {
			identity := protocol.NewJSONObject()
			identity.Set("id", id)
			blocked = blocked || unresolved[terminalTaskIdentity(identity)]
		}
		if !blocked {
			return task, true
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return protocol.NewJSONObject(), false
}

func terminalSpinnerFormatTokens(value float64) string {
	unit := ""
	for _, candidate := range []struct {
		threshold float64
		name      string
	}{{1e12, "t"}, {1e9, "b"}, {1e6, "m"}, {1e3, "k"}} {
		if math.Abs(value) >= candidate.threshold {
			value /= candidate.threshold
			unit = candidate.name
			break
		}
	}
	return strings.TrimSuffix(strconv.FormatFloat(math.Floor(value*10+.5)/10, 'f', 1, 64), ".0") + unit
}

type terminalSpinnerFlexPart struct {
	value string
	width int
	fixed bool
}

// SpinnerAnimationRow is a wrapping Yoga row. Each Text is measured with an
// AtMost constraint and keeps its own word-wrap width after moving to a new
// flex line; flattening its fragments into a single string changes geometry.
func terminalSpinnerFlexWrap(parts []terminalSpinnerFlexPart, width int) []string {
	width = max(1, width)
	grid := [][]*terminalStatusLineCell{}
	x, y, lineHeight := 0, 0, 1
	for _, part := range parts {
		basis := part.width
		if !part.fixed {
			for _, row := range exportWrapWords(ansi.Strip(part.value), width) {
				basis = max(basis, exportWidth(row))
			}
		}
		if x > 0 && x+basis > width {
			y += lineHeight
			x, lineHeight = 0, 1
		}
		basis = min(width, basis)
		painted := terminalMarkdownApplyStyles(part.value, exportWrapWords(ansi.Strip(part.value), max(1, basis)))
		lineHeight = max(lineHeight, len(painted))
		for dy, row := range painted {
			for len(grid) <= y+dy {
				grid = append(grid, make([]*terminalStatusLineCell, width))
			}
			column := x
			for _, cell := range terminalStatusLineParse(row, false, "39") {
				n := uniseg.StringWidth(cell.text)
				if column+n <= width {
					copy := cell
					grid[y+dy][column] = &copy
					for k := 1; k < n; k++ {
						grid[y+dy][column+k] = &terminalStatusLineCell{text: "", style: cell.style}
					}
				}
				column += n
			}
		}
		x += basis
	}
	rows := []string{}
	for _, cells := range grid {
		value := []terminalStatusLineCell{}
		for _, cell := range cells {
			if cell == nil {
				value = append(value, terminalStatusLineCell{text: " ", style: terminalStatusLineStyle{fg: "39"}})
			} else if cell.text != "" {
				value = append(value, *cell)
			}
		}
		rows = append(rows, terminalSpinnerPaintCells(value))
	}
	return rows
}

func terminalSpinnerResponseLines(value string, width int, theme string) []string {
	rows := terminalMarkdownApplyStyles(terminalTaskText(value, theme, "inactive", false, false), exportWrapWords(value, max(1, width-5)))
	for index := range rows {
		prefix := strings.Repeat(" ", 5)
		if index == 0 {
			prefix = terminalTaskText("  ⎿ \u00a0", theme, "inactive", false, false)
		}
		rows[index] = terminalSpinnerPaintCells(terminalStatusLineParse(prefix+rows[index], false, "39"))
	}
	return rows
}

func terminalSpinnerPaintCells(cells []terminalStatusLineCell) string {
	var row strings.Builder
	style := ""
	for _, cell := range cells {
		current := cell.style.sgr()
		if current != style {
			row.WriteString("\x1b[0;" + current + "m")
			style = current
		}
		row.WriteString(cell.text)
	}
	if style != "" {
		row.WriteString("\x1b[0m")
	}
	return row.String()
}
