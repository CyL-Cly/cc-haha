package tui

import (
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/buddy"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

func (s *State) applyTerminalBuddy(raw json.RawMessage) {
	var value protocol.JSONObject
	if value.UnmarshalJSON(raw) != nil || !value.IsObject() {
		return
	}
	if s.buddy == nil {
		s.buddy = &terminalBuddyRuntime{started: time.Now()}
	}
	if pet := value.Get("petAt"); len(pet) > 0 && string(pet) != string(s.buddy.petAt) {
		s.buddy.petAt = append(json.RawMessage(nil), pet...)
		s.buddy.petStart = s.buddy.tick
	}
	s.buddy.value = value.Clone()
	if _, ok := s.buddy.value.Object("companion"); !ok || string(value.Get("muted")) == "true" {
		s.buddy.focused = false
	}
	s.selectedTerminalFooter()
}

func (s *State) tickTerminalBuddy(now time.Time) {
	if s.buddy != nil && !now.Before(s.buddy.started) {
		s.buddy.tick = int(now.Sub(s.buddy.started) / (500 * time.Millisecond))
	}
}

func (s *State) handleTerminalBuddyPanelKey(key Key) (bool, []action) {
	p := s.LocalPanel
	if p == nil || p.props.Kind != "buddy" {
		return false, nil
	}
	if key.Text == "q" || key.Name == "q" {
		return true, []action{{kind: actionLocalPanel, panel: p.result("buddy-close")}}
	}
	// The frozen DOM handler tests the literal "Enter"; the actual terminal
	// parser emits "return", so Enter/Esc/n do not complete this card.
	if key.Name == "ctrl+c" || key.Name == "ctrl+d" {
		return false, nil
	}
	return true, nil
}

func buddyStyle(text, theme, role string, bold, italic, dim, inverse bool) string {
	prefix := terminalBuddyRoleEscape(theme, role)
	if dim {
		prefix = terminalBuddyRoleEscape(theme, "inactive")
	}
	if bold {
		prefix += "\x1b[1m"
	}
	if italic {
		prefix += "\x1b[3m"
	}
	if inverse {
		prefix += "\x1b[7m"
	}
	return prefix + displayText(text) + "\x1b[0m"
}

func terminalBuddyCardLines(raw json.RawMessage, width int, theme string) []string {
	value := presentationObject(raw)
	companion, present := value.Object("companion")
	bones := companion
	if !present {
		bones, _ = value.Object("preview")
	}
	role := buddy.RarityRole(bones.String("rarity"))
	sprite, err := buddy.Sprite(bones, 0)
	if err != nil {
		return wrap(err.Error(), width)
	}
	lines := []string{""}
	appendText := func(value string) {
		for _, row := range buddyWrapStyled(value, max(1, width-2)) {
			lines = append(lines, " "+row)
		}
	}
	if !present {
		appendText("\x1b[1mYou haven't hatched a companion yet!\x1b[0m")
		appendText(buddyStyle("Here's a preview of yours:", theme, "", false, false, true, false))
		lines = append(lines, "")
		for _, line := range sprite {
			appendText(buddyStyle(line, theme, role, false, false, false, false))
		}
		appendText(buddyStyle("A "+bones.String("rarity")+" "+bones.String("species")+" "+buddy.RarityStars(bones.String("rarity")), theme, "", false, true, true, false))
		lines = append(lines, "")
		appendText("Run \x1b[1m/buddy hatch\x1b[0m to bring them to life!")
		appendText(terminalBuddyRoleEscape(theme, "inactive") + "Or type \x1b[1mq\x1b[22m to dismiss.\x1b[0m")
		lines = append(lines, "")
		return lines
	}
	left := []string{}
	column := uniseg.StringWidth(displayText(companion.String("name")))
	for _, line := range sprite {
		column = max(column, uniseg.StringWidth(line))
		left = append(left, buddyStyle(line, theme, role, false, false, false, false))
	}
	left = append(left, buddyStyle(companion.String("name"), theme, role, true, true, false, false))
	right := []string{"\x1b[1mSpecies:\x1b[0m " + buddyStyle(companion.String("species"), theme, role, false, false, false, false), "\x1b[1mRarity:\x1b[0m " + buddyStyle(companion.String("rarity")+" "+buddy.RarityStars(companion.String("rarity")), theme, role, false, false, false, false)}
	if string(companion.Get("shiny")) == "true" {
		right = append(right, buddyStyle("✦ Shiny!", theme, "warning", false, false, false, false))
	}
	right = append(right, buddyStyle(strings.Repeat("─", 20), theme, "", false, false, true, false), "\x1b[1mStats:\x1b[0m")
	stats, _ := companion.Object("stats")
	for _, name := range buddy.Stats {
		right = append(right, buddyStyle(name+":", theme, "", false, false, true, false)+" "+buddyStyle(string(stats.Get(name)), theme, role, false, false, false, false))
	}
	rightWidth := 0
	for _, line := range right {
		rightWidth = max(rightWidth, uniseg.StringWidth(displayText(line)))
	}
	leftWidth := column
	leftMeasure, rightMeasure := float64(column), float64(rightWidth)
	if available := max(2, width-4); column+rightWidth > available {
		leftMeasure = float64(column*available) / float64(column+rightWidth)
		rightMeasure = float64(rightWidth*available) / float64(column+rightWidth)
		leftWidth = int(math.Floor(leftMeasure + .5))
		rightWidth = int(math.Floor(rightMeasure + .5))
	}
	left, right = buddyColumnTexts(left, leftWidth, leftMeasure), buddyColumnTexts(right, rightWidth, rightMeasure)
	column = leftWidth
	pad := max(0, (len(left)-len(right))/2)
	for row := 0; row < max(len(left), len(right)); row++ {
		l, r := "", ""
		if row < len(left) {
			l = left[row]
		}
		if row >= pad && row-pad < len(right) {
			r = right[row-pad]
		}
		lines = append(lines, " "+l+strings.Repeat(" ", max(0, column-uniseg.StringWidth(displayText(l)))+2)+r)
	}
	appendText(buddyStyle(strings.Repeat("─", 40), theme, "", false, false, true, false))
	appendText(buddyStyle("/buddy pet · /buddy mute · /buddy unmute · /buddy release", theme, "", false, false, true, false))
	appendText(buddyStyle("Press q or Enter to dismiss", theme, "", false, false, true, false))
	lines = append(lines, "")
	return lines
}

// Yoga measures at the fractional shrink width, then paints at its rounded
// cell width. A text node's painted soft-wrap tail can overlap the next node;
// use the original measured offsets rather than flattening painted rows.
func buddyColumnTexts(values []string, paintWidth int, measureWidth float64) []string {
	rows := []string{}
	offset := 0
	for _, value := range values {
		if displayText(value) == "" {
			continue
		}
		height := len(exportWrapWords(value, max(1, int(math.Floor(measureWidth)))))
		painted := buddyWrapStyled(value, max(1, paintWidth))
		for len(rows) < offset+max(height, len(painted)) {
			rows = append(rows, "")
		}
		for i, row := range painted {
			rows[offset+i] = row
		}
		offset += height
	}
	return rows[:offset]
}

func buddyWrapStyled(value string, width int) []string {
	rows := exportWrapWords(value, max(1, width))
	offset := 0
	for i, row := range rows {
		cells := uniseg.StringWidth(row)
		rows[i] = ansi.Cut(value, offset, offset+cells)
		offset += cells
	}
	return rows
}

func (s *State) terminalBuddySpriteLines() []string {
	if s.buddy == nil || string(s.buddy.value.Get("muted")) == "true" {
		return nil
	}
	companion, ok := s.buddy.value.Object("companion")
	if !ok {
		return nil
	}
	role := buddy.RarityRole(companion.String("rarity"))
	petAge := s.buddy.tick - s.buddy.petStart
	petting := len(s.buddy.petAt) > 0 && string(s.buddy.petAt) != "0" && petAge >= 0 && petAge < 5
	name := displayText(companion.String("name"))
	focused := s.buddy.focused
	if s.Width < 100 {
		face := buddy.Face(companion)
		heart := ""
		if petting {
			heart = buddyStyle("♥ ", s.Preferences.Theme, "autoAccept", false, false, false, false)
		}
		label := name
		if focused {
			label = " " + name + " "
		}
		line := " " + heart + buddyStyle(face, s.Preferences.Theme, role, true, false, false, false) + " " + buddyStyle(label, s.Preferences.Theme, role, false, true, !focused, focused) + " "
		return []string{strings.Repeat(" ", max(0, s.Width-uniseg.StringWidth(displayText(line)))) + line}
	}
	idle := []int{0, 0, 0, 0, 1, 0, 0, 0, -1, 0, 0, 2, 0, 0, 0}
	frame := idle[s.buddy.tick%len(idle)]
	blink := frame < 0
	if petting {
		frame = s.buddy.tick % max(1, buddy.FrameCount(companion.String("species")))
		blink = false
	}
	sprite, err := buddy.Sprite(companion, max(frame, 0))
	if err != nil {
		return nil
	}
	column := max(12, uniseg.StringWidth(name)+2)
	lines := []string{}
	if petting {
		hearts := []string{"   ♥    ♥   ", "  ♥  ♥   ♥  ", " ♥   ♥  ♥   ", "♥  ♥      ♥ ", "·    ·   ·  "}
		line := hearts[petAge]
		lines = append(lines, centerBuddy(buddyStyle(line, s.Preferences.Theme, "autoAccept", false, false, false, false), column))
	}
	for _, line := range sprite {
		if blink {
			line = strings.ReplaceAll(line, companion.String("eye"), "-")
		}
		lines = append(lines, centerBuddy(buddyStyle(line, s.Preferences.Theme, role, false, false, false, false), column))
	}
	if focused {
		name = " " + name + " "
	}
	lines = append(lines, centerBuddy(buddyStyle(name, s.Preferences.Theme, role, focused, true, !focused, focused), column))
	return lines
}

func centerBuddy(value string, width int) string {
	padding := max(0, width-uniseg.StringWidth(displayText(value)))
	return " " + strings.Repeat(" ", padding/2) + value + strings.Repeat(" ", padding-padding/2) + " "
}

func (s *State) buddyReservedColumns() int {
	if s.Width < 100 {
		return 0
	}
	lines := s.terminalBuddySpriteLines()
	width := 0
	for _, line := range lines {
		width = max(width, uniseg.StringWidth(displayText(line)))
	}
	return width
}

func (s *State) joinBuddyEditor(input []string, fullscreen bool) ([]string, int) {
	sprite := s.terminalBuddySpriteLines()
	if len(sprite) == 0 {
		return input, 0
	}
	if s.Width < 100 {
		if fullscreen {
			return append(sprite, input...), len(sprite)
		}
		return append(input, sprite...), 0
	}
	rows := max(len(input), len(sprite))
	width := s.Width - s.buddyReservedColumns()
	result := make([]string, rows)
	for i := 0; i < rows; i++ {
		left, right := "", ""
		if j := i - (rows - len(input)); j >= 0 {
			left = input[j]
		}
		if j := i - (rows - len(sprite)); j >= 0 {
			right = sprite[j]
		}
		result[i] = left + strings.Repeat(" ", max(0, width-uniseg.StringWidth(displayText(left)))) + right
	}
	return result, rows - len(input)
}
