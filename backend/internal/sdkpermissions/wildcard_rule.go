package sdkpermissions

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

func replaceRuleUnits(input, pattern, replacement []uint16) []uint16 {
	out := []uint16{}
	for i := 0; i < len(input); {
		match := i+len(pattern) <= len(input)
		if match {
			for j, c := range pattern {
				if input[i+j] != c {
					match = false
					break
				}
			}
		}
		if match {
			out = append(out, replacement...)
			i += len(pattern)
		} else {
			out = append(out, input[i])
			i++
		}
	}
	return out
}
func asciiRuleUnits(s string) []uint16 {
	out := []uint16{}
	for _, c := range s {
		out = append(out, uint16(c))
	}
	return out
}

// MatchWildcardRule constructs the original shellRuleMatching expression in
// native code. The isolated language VM supplies only generic RegExp semantics,
// including UTF-16, dollar anchors and dotAll; it has no host/IO capabilities.
func MatchWildcardRule(pattern, value json.RawMessage) (bool, error) {
	units, err := protocol.DecodeJavaScriptJSONString(pattern)
	if err != nil {
		return false, err
	}
	valueUnits, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return false, err
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	star, slash := asciiRuleUnits("\x00ESCAPED_STAR\x00"), asciiRuleUnits("\x00ESCAPED_BACKSLASH\x00")
	processed := []uint16{}
	for i := 0; i < len(units); i++ {
		c := units[i]
		if c == '\\' && i+1 < len(units) {
			if units[i+1] == '*' {
				processed = append(processed, star...)
				i++
				continue
			}
			if units[i+1] == '\\' {
				processed = append(processed, slash...)
				i++
				continue
			}
		}
		processed = append(processed, c)
	}
	expression := []uint16{}
	stars := 0
	for _, c := range processed {
		if c == '*' {
			stars++
			expression = append(expression, '.', '*')
		} else {
			if strings.ContainsRune(`.+?^${}()|[]\'"`, rune(c)) {
				expression = append(expression, '\\')
			}
			expression = append(expression, c)
		}
	}
	expression = replaceRuleUnits(expression, star, []uint16{'\\', '*'})
	expression = replaceRuleUnits(expression, slash, []uint16{'\\', '\\'})
	if stars == 1 && len(expression) >= 3 && expression[len(expression)-3] == ' ' && expression[len(expression)-2] == '.' && expression[len(expression)-1] == '*' {
		expression = append(expression[:len(expression)-3], asciiRuleUnits("( .*)?")...)
	}
	expression = append(append([]uint16{'^'}, expression...), '$')
	vm := goja.New()
	// Goja's RegExp implementation converts a lone UTF-16 surrogate to the
	// replacement rune. Map each surrogate unit to a distinct valid scalar on
	// both sides so that non-/u Source matching does not merge those identities.
	scalarUnits := func(units []uint16) string {
		out := make([]rune, len(units))
		for i, c := range units {
			out[i] = rune(c)
			if c >= 0xD800 && c <= 0xDFFF {
				out[i] += 0x10000
			}
		}
		return string(out)
	}
	patternJSON, _ := json.Marshal(scalarUnits(expression))
	valueJSON, _ := json.Marshal(scalarUnits(valueUnits))
	if err = vm.Set("patternJSON", string(patternJSON)); err != nil {
		return false, err
	}
	if err = vm.Set("valueJSON", string(valueJSON)); err != nil {
		return false, err
	}
	result, err := vm.RunString(`new RegExp(JSON.parse(patternJSON), 's').test(JSON.parse(valueJSON))`)
	if err != nil {
		return false, err
	}
	return result.ToBoolean(), nil
}
