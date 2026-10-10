package sessionprojection

import (
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func asciiLetter(unit uint16) bool { return unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' }
func word(unit uint16) bool        { return asciiLetter(unit) || unit >= '0' && unit <= '9' || unit == '_' }
func whitespace(unit uint16) bool {
	return unit >= 9 && unit <= 13 || unit == 0x20 || unit == 0xa0 || unit == 0x1680 || unit >= 0x2000 && unit <= 0x200a || unit == 0x2028 || unit == 0x2029 || unit == 0x202f || unit == 0x205f || unit == 0x3000 || unit == 0xfeff
}
func trim(units []uint16) []uint16 {
	for len(units) != 0 && whitespace(units[0]) {
		units = units[1:]
	}
	for len(units) != 0 && whitespace(units[len(units)-1]) {
		units = units[:len(units)-1]
	}
	return units
}
func normalized(units []uint16) []uint16 {
	var result []uint16
	space := false
	for _, unit := range trim(units) {
		if whitespace(unit) {
			space = true
		} else {
			if space {
				result = append(result, ' ')
			}
			space = false
			result = append(result, unit)
		}
	}
	return result
}
func matching(units []uint16, start int, pattern string, fold bool) bool {
	if start < 0 || start+len(pattern) > len(units) {
		return false
	}
	for index, char := range pattern {
		unit := units[start+index]
		if fold && unit >= 'A' && unit <= 'Z' {
			unit += 'a' - 'A'
		}
		if unit != uint16(char) {
			return false
		}
	}
	return true
}
func find(units []uint16, start int, pattern string, fold bool) int {
	for index := start; index+len(pattern) <= len(units); index++ {
		if matching(units, index, pattern, fold) {
			return index
		}
	}
	return -1
}
func decodeXML(units []uint16) []uint16 {
	for _, replacement := range []struct {
		from string
		to   uint16
	}{{"&lt;", '<'}, {"&gt;", '>'}, {"&quot;", '"'}, {"&#39;", '\''}, {"&amp;", '&'}} {
		var decoded []uint16
		for index := 0; index < len(units); {
			if matching(units, index, replacement.from, false) {
				decoded = append(decoded, replacement.to)
				index += len(replacement.from)
			} else {
				decoded = append(decoded, units[index])
				index++
			}
		}
		units = decoded
	}
	return units
}
func xmlTag(units []uint16, tag string, attributes bool) []uint16 {
	for offset := 0; ; {
		start := find(units, offset, "<"+tag, true)
		if start < 0 {
			return nil
		}
		open := start + len(tag) + 1
		if attributes && open < len(units) && whitespace(units[open]) {
			for open < len(units) && units[open] != '>' {
				open++
			}
		}
		if open < len(units) && units[open] == '>' {
			end := find(units, open+1, "</"+tag+">", true)
			if end >= 0 {
				return decodeXML(trim(units[open+1 : end]))
			}
		}
		offset = start + 1
	}
}
func cleanTitle(units []uint16) []uint16 {
	if command := xmlTag(units, "command-name", true); len(command) != 0 {
		command = append([]uint16(nil), command...)
		if args := xmlTag(units, "command-args", true); len(args) != 0 {
			command = append(append(command, ' '), args...)
		}
		return normalized(command)
	}
	var stripped []uint16
	for index := 0; index < len(units); {
		if units[index] == '<' && index+1 < len(units) && units[index+1] >= 'a' && units[index+1] <= 'z' {
			nameEnd := index + 2
			for nameEnd < len(units) && (word(units[nameEnd]) || units[nameEnd] == '-') {
				nameEnd++
			}
			open := nameEnd
			if open < len(units) && whitespace(units[open]) {
				for open < len(units) && units[open] != '>' {
					open++
				}
			}
			if open < len(units) && units[open] == '>' {
				tag := string(utf16.Decode(units[index+1 : nameEnd]))
				if end := find(units, open+1, "</"+tag+">", false); end >= 0 {
					index = end + len(tag) + 3
					if index < len(units) && units[index] == '\n' {
						index++
					}
					stripped = append(stripped, ' ')
					continue
				}
			}
		}
		stripped = append(stripped, units[index])
		index++
	}
	return normalized(stripped)
}
func shorten(units []uint16) json.RawMessage {
	if len(units) == 0 {
		return null()
	}
	if len(units) > 80 {
		units = append(append([]uint16(nil), units[:80]...), '.', '.', '.')
	}
	return quoted(units)
}
func goalTitle(entry protocol.JSONObject) json.RawMessage {
	if !equal(entry.Get("type"), "system") || !equal(entry.Get("subtype"), "local_command") {
		return null()
	}
	units, ok := stringUnits(entry.Get("content"))
	if !ok {
		return null()
	}
	command := xmlTag(units, "command-name", false)
	if len(command) != 0 && command[0] == '/' {
		command = command[1:]
	}
	if !matching(command, 0, "goal", false) || len(command) != 4 {
		return null()
	}
	args := trim(xmlTag(units, "command-args", false))
	if len(args) == 0 || matching(args, 0, "clear", true) && (len(args) == 5 || !word(args[5])) {
		return null()
	}
	return shorten(cleanTitle(append([]uint16{'/', 'g', 'o', 'a', 'l', ' '}, args...)))
}

type TypeError struct{ Message string }

func (err *TypeError) Error() string { return err.Message }
func userTitle(raw json.RawMessage) (json.RawMessage, error) {
	units, ok := stringUnits(raw)
	if !ok && len(raw) != 0 && raw[0] == '[' {
		var blocks []json.RawMessage
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, err
		}
		for _, block := range blocks {
			if isNull(block) {
				return nil, &TypeError{"null is not an object (evaluating 'block.type')"}
			}
			fields := object(block)
			if equal(fields.Get("type"), "text") {
				if candidate, valid := stringUnits(fields.Get("text")); valid {
					units, ok = candidate, true
					break
				}
			}
		}
	}
	if !ok {
		return null(), nil
	}
	return shorten(cleanTitle(units)), nil
}
func desanitize(project, platform string) string {
	separator := "/"
	if platform == "win32" {
		separator = "\\"
	}
	if len(project) >= 3 && asciiLetter(uint16(project[0])) && project[1:3] == "--" && (len(project) == 3 || !strings.ContainsAny(project[3:], "\r\n\u2028\u2029")) {
		return project[:1] + ":\\" + strings.ReplaceAll(project[3:], "-", "\\")
	}
	return strings.ReplaceAll(project, "-", separator)
}
