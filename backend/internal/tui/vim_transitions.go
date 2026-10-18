package tui

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var vimDigit = regexp.MustCompile(`[0-9]`)
var vimLeadingCount = regexp.MustCompile(`[1-9]`)

func vimParseCount(value string) int {
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return -1
	}
	count, err := strconv.ParseFloat(value[:end], 64)
	if err != nil {
		return vimMaximumCount
	}
	if count >= float64(math.MaxInt) {
		return math.MaxInt
	}
	return int(count)
}
func vimCountDigits(value string) string {
	count := vimParseCount(value)
	if count < 0 {
		return "NaN"
	}
	return strconv.Itoa(min(count, vimMaximumCount))
}
func vimProduct(left, right int) int {
	if left < 0 || right < 0 {
		return -1
	}
	return left * right
}
func vimOperator(input string) string {
	return map[string]string{"d": "delete", "c": "change", "y": "yank"}[input]
}
func vimIsFind(input string) bool {
	return input == "f" || input == "F" || input == "t" || input == "T"
}
func vimIsMotion(input string) bool {
	return input == "h" || input == "l" || input == "j" || input == "k" || input == "w" || input == "b" || input == "e" || input == "W" || input == "B" || input == "E" || input == "0" || input == "^" || input == "$"
}

func (v *VimEditor) normalInput(input string, count, columns int) (bool, bool) {
	if operator := vimOperator(input); operator != "" {
		v.Command = vimCommand{Type: "operator", Operator: operator, Count: count}
		return true, false
	}
	if vimIsMotion(input) {
		v.Offset = v.cursor(columns).motion(input, count).offset
		return true, true
	}
	if vimIsFind(input) {
		v.Command = vimCommand{Type: "find", Find: input, Count: count}
		return true, false
	}
	switch input {
	case "g":
		v.Command = vimCommand{Type: "g", Count: count}
		return true, false
	case "r":
		v.Command = vimCommand{Type: "replace", Count: count}
		return true, false
	case ">", "<":
		v.Command = vimCommand{Type: "indent", Direction: input, Count: count}
		return true, false
	case "~":
		v.toggle(count)
	case "x":
		v.deleteCharacters(count, columns)
	case "J":
		v.join(count, columns)
	case "p", "P":
		v.paste(input == "p", count, columns)
	case "D", "C":
		operator := "delete"
		if input == "C" {
			operator = "change"
		}
		v.operatorMotion(operator, "$", 1, columns)
	case "Y":
		v.lineOperator("yank", count, columns)
	case "G":
		cursor := v.cursor(columns)
		if count == 1 {
			v.Offset = cursor.lastLine().offset
		} else {
			v.Offset = cursor.goLine(count).offset
		}
	case ".":
		v.replay(columns)
	case ";", ",":
		if v.LastFind != nil {
			find := v.LastFind.Type
			if input == "," {
				find = map[string]string{"f": "F", "F": "f", "t": "T", "T": "t"}[find]
			}
			if offset, ok := v.cursor(columns).find(v.LastFind.Character, find, count); ok {
				v.Offset = offset
			}
		}
	case "u":
		if len(v.Undo) > 0 {
			snapshot := v.Undo[len(v.Undo)-1]
			v.Undo = v.Undo[:len(v.Undo)-1]
			v.Text, v.Offset = vimClone(snapshot.Text), snapshot.Offset
		}
	case "i":
		v.enterInsert(v.cursor(columns).offset)
	case "I":
		v.enterInsert(v.cursor(columns).firstNonBlank().offset)
	case "a":
		cursor := v.cursor(columns)
		v.enterInsert(cursor.right().offset)
	case "A":
		cursor := v.cursor(columns)
		v.enterInsert(cursor.lineEnd(cursor.offset))
	case "o", "O":
		direction := "below"
		if input == "O" {
			direction = "above"
		}
		v.openLine(direction, columns)
	default:
		return false, false
	}
	return true, true
}
func (v *VimEditor) operatorInput(operator string, count int, input string, columns int) (bool, bool) {
	if input == "i" || input == "a" {
		scope := "inner"
		if input == "a" {
			scope = "around"
		}
		v.Command = vimCommand{Type: "operatorTextObj", Operator: operator, Scope: scope, Count: count}
		return true, false
	}
	if vimIsFind(input) {
		v.Command = vimCommand{Type: "operatorFind", Operator: operator, Find: input, Count: count}
		return true, false
	}
	if vimIsMotion(input) {
		v.operatorMotion(operator, input, count, columns)
		return true, true
	}
	if input == "G" {
		v.operatorG(operator, "G", count, columns)
		return true, true
	}
	if input == "g" {
		v.Command = vimCommand{Type: "operatorG", Operator: operator, Count: count}
		return true, false
	}
	return false, false
}
func (v *VimEditor) transition(input string, columns int) {
	state := v.Command
	if state.Type == "" {
		state.Type = "idle"
	}
	executed, next := false, false
	switch state.Type {
	case "idle":
		if vimLeadingCount.MatchString(input) {
			v.Command = vimCommand{Type: "count", Digits: input}
			next = true
		} else if input == "0" {
			cursor := v.cursor(columns)
			v.Offset = cursor.lineStart(cursor.offset)
			executed = true
		} else {
			recognized, exec := v.normalInput(input, 1, columns)
			next, executed = recognized && !exec, exec
		}
	case "count":
		if vimDigit.MatchString(input) {
			v.Command = vimCommand{Type: "count", Digits: vimCountDigits(state.Digits + input)}
			next = true
		} else {
			recognized, exec := v.normalInput(input, vimParseCount(state.Digits), columns)
			next, executed = recognized && !exec, exec
			if !recognized {
				v.Command = vimCommand{Type: "idle"}
				next = true
			}
		}
	case "operator":
		if input == state.Operator[:1] {
			v.lineOperator(state.Operator, state.Count, columns)
			executed = true
		} else if vimDigit.MatchString(input) {
			v.Command = vimCommand{Type: "operatorCount", Operator: state.Operator, Count: state.Count, Digits: input}
			next = true
		} else {
			recognized, exec := v.operatorInput(state.Operator, state.Count, input, columns)
			next, executed = recognized && !exec, exec
			if !recognized {
				v.Command = vimCommand{Type: "idle"}
				next = true
			}
		}
	case "operatorCount":
		if vimDigit.MatchString(input) {
			state.Digits = vimCountDigits(state.Digits + input)
			v.Command = state
			next = true
		} else {
			recognized, exec := v.operatorInput(state.Operator, vimProduct(state.Count, vimParseCount(state.Digits)), input, columns)
			next, executed = recognized && !exec, exec
			if !recognized {
				v.Command = vimCommand{Type: "idle"}
				next = true
			}
		}
	case "operatorFind":
		v.operatorFind(state.Operator, state.Find, vimUnits(input), state.Count, columns)
		executed = true
	case "operatorTextObj":
		if len(input) == 1 && strings.Contains("wW\"'`()b[]{}B<>", input) {
			v.operatorObject(state.Operator, state.Scope, input, state.Count)
			executed = true
		} else {
			v.Command = vimCommand{Type: "idle"}
			next = true
		}
	case "find":
		if offset, ok := v.cursor(columns).find(vimUnits(input), state.Find, state.Count); ok {
			v.Offset = offset
			v.LastFind = &vimFind{state.Find, vimUnits(input)}
		}
		executed = true
	case "g":
		if input == "j" || input == "k" {
			v.Offset = v.cursor(columns).motion("g"+input, state.Count).offset
			executed = true
		} else if input == "g" {
			v.Offset = v.cursor(columns).goLine(state.Count).offset
			executed = true
		} else {
			v.Command = vimCommand{Type: "idle"}
			next = true
		}
	case "operatorG":
		if input == "j" || input == "k" {
			v.operatorMotion(state.Operator, "g"+input, state.Count, columns)
			executed = true
		} else if input == "g" {
			v.operatorG(state.Operator, "gg", state.Count, columns)
			executed = true
		} else {
			v.Command = vimCommand{Type: "idle"}
			next = true
		}
	case "replace":
		if input != "" {
			v.replace(vimUnits(input), state.Count)
			executed = true
		} else {
			v.Command = vimCommand{Type: "idle"}
			next = true
		}
	case "indent":
		if input == state.Direction {
			v.indent(state.Direction, state.Count, columns)
			executed = true
		} else {
			v.Command = vimCommand{Type: "idle"}
			next = true
		}
	}
	if v.Mode == "NORMAL" && !next && executed {
		v.Command = vimCommand{Type: "idle"}
	}
}

// HandleKey returns false only at the original base-input delegation boundary.
// The caller still owns submission, command menus, permission UI and Harness.
func (v *VimEditor) HandleKey(key Key, columns int) bool {
	if strings.HasPrefix(key.Name, "ctrl+") || key.Name == "enter" {
		if key.Name != "enter" && v.rawEditingNeeded() {
			return v.rawBaseKey(key, columns)
		}
		return false
	}
	if key.Name == "esc" {
		v.escape()
		return true
	}
	if v.Mode == "INSERT" {
		if key.Name == "backspace" || key.Name == "delete" {
			if len(v.InsertedText) > 0 {
				v.InsertedText = v.InsertedText[:max(0, len(v.InsertedText)-max(1, len(vimLast(v.InsertedText))))]
			}
		} else {
			v.InsertedText = append(v.InsertedText, vimUnits(key.Text)...)
		}
		if v.rawEditingNeeded() {
			return v.rawBaseKey(key, columns)
		}
		return false
	}
	if v.Command.Type == "idle" && (key.Name == "left" || key.Name == "right" || key.Name == "up" || key.Name == "down") {
		if v.rawEditingNeeded() {
			return v.rawBaseKey(key, columns)
		}
		return false
	}
	input := key.Text
	expectsMotion := v.Command.Type == "idle" || v.Command.Type == "count" || v.Command.Type == "operator" || v.Command.Type == "operatorCount"
	switch key.Name {
	case "left":
		input = "h"
	case "right":
		input = "l"
	case "up":
		input = "k"
	case "down":
		input = "j"
	case "backspace":
		if expectsMotion {
			input = "h"
		}
	case "delete":
		if expectsMotion && v.Command.Type != "count" {
			input = "x"
		}
	}
	oldCommand := v.Command.Type
	v.transition(input, columns)
	if key.Text == "?" && oldCommand == "idle" {
		v.change(vimUnits("?"))
	}
	return true
}
