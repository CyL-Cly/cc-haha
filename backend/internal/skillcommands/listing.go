package skillcommands

import (
	"encoding/json"
	"math"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
)

func listingQuoted(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func listingJoin(parts ...json.RawMessage) (json.RawMessage, error) {
	return protocol.JoinJavaScriptValuesJSON(parts, "")
}
func listingWidth(raw json.RawMessage) int {
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	return ansi.StringWidth(string(utf16.Decode(units)))
}
func listingDescription(command protocol.JSONObject) (json.RawMessage, error) {
	description, e := protocol.ExactJavaScriptStringJSON(command.Get("description"))
	if e != nil {
		return nil, e
	}
	if when := command.Get("whenToUse"); pluginTruthy(when) {
		converted, e := protocol.ExactJavaScriptStringJSON(when)
		if e != nil {
			return nil, e
		}
		description, e = listingJoin(description, listingQuoted(" - "), converted)
		if e != nil {
			return nil, e
		}
	}
	units, e := protocol.DecodeJavaScriptJSONString(description)
	if e != nil {
		return nil, e
	}
	if len(units) > 250 {
		description = protocol.QuoteJavaScriptUTF16(append(append([]uint16{}, units[:249]...), 0x2026))
	}
	return description, nil
}
func listingNumber(value string) float64 {
	value = trimJS(value)
	if value == "" {
		return 0
	}
	if len(value) > 2 && value[0] == '0' {
		base := 0
		switch value[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			n, e := strconv.ParseUint(value[2:], base, 64)
			if e != nil {
				return math.NaN()
			}
			return float64(n)
		}
	}
	n, e := strconv.ParseFloat(value, 64)
	if e != nil {
		return math.NaN()
	}
	return n
}
func FormatWithinBudget(commands []protocol.JSONObject, contextWindow float64, budgetEnvironment string) (json.RawMessage, error) {
	if len(commands) == 0 {
		return listingQuoted(""), nil
	}
	budget := listingNumber(budgetEnvironment)
	if budget == 0 || math.IsNaN(budget) {
		budget = 8000
		if contextWindow != 0 && !math.IsNaN(contextWindow) {
			budget = math.Floor(contextWindow * 4 * 0.01)
		}
	}
	entries, descriptions := make([]json.RawMessage, len(commands)), make([]json.RawMessage, len(commands))
	fullTotal, bundledChars, restNames := len(commands)-1, 0, 0
	restCount := 0
	for i, command := range commands {
		description, e := listingDescription(command)
		if e != nil {
			return nil, e
		}
		descriptions[i] = description
		entry, e := listingJoin(listingQuoted("- "), command.Get("name"), listingQuoted(": "), description)
		if e != nil {
			return nil, e
		}
		entries[i] = entry
		fullTotal += listingWidth(entry)
		if command.String("source") == "bundled" && command.String("type") == "prompt" {
			bundledChars += listingWidth(entry) + 1
		} else {
			restCount++
			restNames += listingWidth(command.Get("name")) + 4
		}
	}
	if float64(fullTotal) > budget && restCount > 0 {
		restNames += restCount - 1
		maxDescription := math.Floor((budget - float64(bundledChars+restNames)) / float64(restCount))
		for i, command := range commands {
			if command.String("source") == "bundled" && command.String("type") == "prompt" {
				continue
			}
			if maxDescription < 20 {
				entry, e := listingJoin(listingQuoted("- "), command.Get("name"))
				if e != nil {
					return nil, e
				}
				entries[i] = entry
				continue
			}
			description := descriptions[i]
			if float64(listingWidth(description)) > maxDescription {
				units, _ := protocol.DecodeJavaScriptJSONString(description)
				description = listingQuoted(ansi.Truncate(string(utf16.Decode(units)), int(maxDescription), "…"))
			}
			entry, e := listingJoin(listingQuoted("- "), command.Get("name"), listingQuoted(": "), description)
			if e != nil {
				return nil, e
			}
			entries[i] = entry
		}
	}
	return protocol.JoinJavaScriptValuesJSON(entries, "\n")
}

func Listed(command protocol.JSONObject) bool {
	if command.String("type") != "prompt" || pluginTruthy(command.Get("disableModelInvocation")) || command.String("source") == "builtin" {
		return false
	}
	loaded := command.String("loadedFrom")
	return loaded == "bundled" || loaded == "skills" || loaded == "commands_DEPRECATED" || pluginTruthy(command.Get("hasUserSpecifiedDescription")) || pluginTruthy(command.Get("whenToUse"))
}
