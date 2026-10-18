package application

import (
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var terminalPluginDecimal = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// This is the JSON payload of Source Number(input), after JSON.stringify.
// Invalid Number inputs remain their original string for actual validation.
func terminalPluginNumberValue(input string) json.RawMessage {
	value := protocol.TrimJavaScriptString(input)
	if value == "Infinity" || value == "+Infinity" || value == "-Infinity" {
		return json.RawMessage("null")
	}
	var number float64
	valid := false
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
			digits := value[2:]
			alphabet := map[int]string{16: "0123456789abcdef", 8: "01234567", 2: "01"}[base]
			if strings.IndexFunc(strings.ToLower(digits), func(r rune) bool { return !strings.ContainsRune(alphabet, r) }) < 0 {
				integer, ok := new(big.Int).SetString(digits, base)
				if ok {
					number, _ = new(big.Float).SetInt(integer).Float64()
					valid = true
				}
			}
		}
	}
	if !valid && terminalPluginDecimal.MatchString(value) {
		parsed, err := strconv.ParseFloat(value, 64)
		if err == nil || math.IsInf(parsed, 0) {
			number, valid = parsed, true
		}
	}
	if !valid {
		return raw(input)
	}
	if math.IsInf(number, 0) {
		return json.RawMessage("null")
	}
	if number == 0 {
		return json.RawMessage("0")
	}
	return raw(number)
}
